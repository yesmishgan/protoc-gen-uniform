package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"path/filepath"
	"reflect"
	"strconv"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/pluginpb"
)

const (
	version = "v0.0.1"
)

// errors
var (
	errMultiplyService = errors.New("files with multiply services aren't supported")
	errValidationFails = errors.New("proto file did not pass validation")
)

// imported packages
var (
	contextPackage    = protogen.GoImportPath("context")
	grpcPackage       = protogen.GoImportPath("google.golang.org/grpc")
	runtimePackage    = protogen.GoImportPath("github.com/grpc-ecosystem/grpc-gateway/v2/runtime")
	middlewarePackage = protogen.GoImportPath("github.com/grpc-ecosystem/go-grpc-middleware")
)

// flags
var (
	showVersion     = flag.Bool("version", false, "print the version and exit")
	registerGateway = flag.Bool("register-gateway", true, "enable register handler servers in RegisterGateway")
)

func main() {
	flag.Parse()
	if *showVersion {
		fmt.Printf("protoc-gen-bomboglot %v\n", version)
		return
	}

	protogen.Options{
		ParamFunc: flag.CommandLine.Set,
	}.Run(func(p *protogen.Plugin) error {
		p.SupportedFeatures = uint64(pluginpb.CodeGeneratorResponse_FEATURE_PROTO3_OPTIONAL)

		validator := &uniqPackageValidator{
			dir2pkg: make(map[string]string),
		}

		for _, f := range p.FilesByPath {
			if !f.Generate {
				continue
			}

			validator.checkFile(p, f)
			validateFile(p, f)
			generateFile(p, f)
		}

		return nil
	})
}

type uniqPackageValidator struct {
	dir2pkg map[string]string
}

func (pv *uniqPackageValidator) checkFile(p *protogen.Plugin, f *protogen.File) {
	dir := filepath.Dir(f.Proto.GetName())
	if pkg, ok := pv.dir2pkg[dir]; ok && f.Proto.GetPackage() != pkg {
		log.Printf(
			"ERROR: dir %q contains protos with different packages (%s, %s, ...)\n",
			dir, pkg, f.Proto.GetPackage(),
		)
		p.Error(errValidationFails)
	}

	pv.dir2pkg[dir] = f.Proto.GetPackage()
}

func validateFile(p *protogen.Plugin, f *protogen.File) {
	if f.Proto.Package == nil {
		p.Error(fmt.Errorf("ERROR: proto file %q has no package, which is required",
			f.Proto.GetName(),
		))
	}
}

func generateFile(p *protogen.Plugin, f *protogen.File) {
	if len(f.Services) == 0 {
		return
	}

	// Supports only 1 service for each proto file
	if len(f.Services) > 1 {
		p.Error(errMultiplyService)
		return
	}

	var (
		service = f.Services[0]

		descName  = service.GoName + "ServiceDesc"
		proxyName = "proxy" + service.GoName + "Server"
	)

	// Generate new file
	g := p.NewGeneratedFile(f.GeneratedFilenamePrefix+".pb.bomboglot.go", f.GoImportPath)
	g.P("// Code generated by protoc-gen-bomboglot. DO NOT EDIT.")
	g.P("// versions:")
	protocVersion := "(unknown)"
	if v := p.Request.GetCompilerVersion(); v != nil {
		protocVersion = fmt.Sprintf("v%d.%d.%d", v.GetMajor(), v.GetMinor(), v.GetPatch())
	}
	// Set version of protoc-gen-bomboglot and protoc
	g.P("// \tprotoc-gen-bomboglot: ", version)
	g.P("// \tprotoc:             ", protocVersion)
	if f.Proto != nil && f.Proto.Name != nil {
		g.P("// source: ", *f.Proto.Name)
	}
	g.P()
	g.P("package ", f.GoPackageName)

	g.P()
	g.P(`import _ "embed"`)
	g.P()

	g.P("//go:embed ", trimPathAndExt(f.Proto.GetName()), `.swagger.json`)
	g.P("var swaggerJSON []byte")

	g.P("// ", descName, " is description for the ", service.GoName, "Server.")
	g.P("type ", descName, " struct {")
	g.P("svc ", service.GoName, "Server")
	// Interceptor
	g.P("i ", g.QualifiedGoIdent(grpcPackage.Ident("UnaryServerInterceptor")))
	g.P("}")
	g.P()

	// New for ServiceDesc
	g.P("func New", descName, "(i ", service.GoName, "Server) ", "*", descName, " {")
	g.P("return &", descName, "{svc: i}")
	g.P("}")
	g.P()

	// Getter swagger.json
	g.P("func(d *", descName, ") Swagger() []byte {")
	g.P(`return swaggerJSON`)
	g.P("}")
	g.P()

	g.P("func(d *", descName, ") RegisterGRPC(s *", g.QualifiedGoIdent(grpcPackage.Ident("Server")), ") {")
	g.P("Register", service.GoName, "Server(s, d.svc)")
	g.P("}")
	g.P()

	g.P("func(d *", descName, ") RegisterGateway(ctx ", g.QualifiedGoIdent(contextPackage.Ident("Context")), ", mux *", g.QualifiedGoIdent(runtimePackage.Ident("ServeMux")), ") error {")

	if *registerGateway || methodsHaveHttpOptions(service.Methods) {
		g.P("if d.i == nil {")
		g.P("return Register", service.GoName, "HandlerServer(ctx, mux, d.svc)")
		g.P("}")
		g.P("return Register", service.GoName, "HandlerServer(ctx, mux, &", proxyName, "{")
		g.P(service.GoName, "Server: d.svc,")
		g.P("interceptor: d.i,")
		g.P("})")
	} else {
		g.P("return nil")
	}
	g.P("}")
	g.P()

	g.P("// WithHTTPUnaryInterceptor adds GRPC Server Interceptor for HTTP gateway requests. Call again for multiple Interceptors.")
	g.P("func(d *", descName, ") WithHTTPUnaryInterceptor(u ", g.QualifiedGoIdent(grpcPackage.Ident("UnaryServerInterceptor")), ") {")
	g.P("if d.i == nil {")
	g.P("d.i = u")
	g.P("} else {")
	g.P("d.i = ", g.QualifiedGoIdent(middlewarePackage.Ident("ChainUnaryServer")), "(d.i, u)")
	g.P("}")
	g.P("}")
	g.P()

	g.P("type ", proxyName, " struct {")
	g.P(service.GoName, "Server")
	g.P("interceptor ", g.QualifiedGoIdent(grpcPackage.Ident("UnaryServerInterceptor")))
	g.P("}")
	g.P()

	for _, method := range service.Methods {
		// Create proxy method for GRPC methods with interceptors
		// Condition is the same as in grpc-go plugin:
		// https://github.com/grpc/grpc-go/blob/master/cmd/protoc-gen-go-grpc/grpc.go#L461
		if !method.Desc.IsStreamingClient() && !method.Desc.IsStreamingServer() {
			g.P("func (p *", proxyName, ") ", method.GoName, "(ctx ", contextPackage.Ident("Context"), ", req *", method.Input.GoIdent, ") (*", method.Output.GoIdent, ", error) {")
			g.P("info := &", grpcPackage.Ident("UnaryServerInfo"), "{")
			g.P("Server: p.", service.GoName, "Server,")
			g.P("FullMethod: ", strconv.Quote(fmt.Sprintf("/%s/%s", service.Desc.FullName(), method.Desc.Name())), ",")
			g.P("}")

			g.P("handler := func(ctx ", contextPackage.Ident("Context"), ", req any) (any, error) {")
			g.P("return p.", service.GoName, "Server.", method.GoName, "(ctx, req.(*", method.Input.GoIdent, "))")
			g.P("}")
			g.P("resp, err := p.interceptor(ctx, req, info, handler)")
			g.P("if err != nil || resp == nil {")
			g.P("return nil, err")
			g.P("}")
			g.P("return resp.(*", method.Output.GoIdent, "), nil")
			g.P("}")
			g.P()
		}
	}
}

func trimPathAndExt(fName string) string {
	f := filepath.Base(fName)
	ext := filepath.Ext(f)
	return f[:len(f)-len(ext)]
}

// Check for an option api.http
func methodsHaveHttpOptions(methods []*protogen.Method) bool {
	for _, method := range methods {
		ext := proto.GetExtension(method.Desc.Options(), annotations.E_Http)
		// Returning interface{}
		if reflect.ValueOf(ext).IsNil() {
			continue
		}
		if _, ok := ext.(*annotations.HttpRule); ok {
			return true
		}
	}

	return false
}