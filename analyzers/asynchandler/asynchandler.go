package asynchandler

import (
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

var Analyzer = &analysis.Analyzer{
	Name: "asynchandler",
	Doc: "reports go statements inside Kubernetes event handler callbacks that may spawn " +
		"queue.Add calls invisible to DSK handler tracking. " +
		"Detects cache.ResourceEventHandlerFuncs and cache.ResourceEventHandlerDetailedFuncs " +
		"composite literals, and named types whose OnAdd/OnUpdate/OnDelete methods implement " +
		"cache.ResourceEventHandler.",
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      run,
}

func run(pass *analysis.Pass) (any, error) {
	insp := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)

	rehInterface := resourceEventHandlerInterface(pass)

	nodeFilter := []ast.Node{
		(*ast.CompositeLit)(nil),
		(*ast.FuncDecl)(nil),
	}

	insp.Preorder(nodeFilter, func(n ast.Node) {
		switch node := n.(type) {
		case *ast.CompositeLit:
			if !isKnownHandlerFuncsType(pass, node) {
				return
			}
			for _, elt := range node.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				ident, ok := kv.Key.(*ast.Ident)
				if !ok {
					continue
				}
				if ident.Name != "AddFunc" && ident.Name != "UpdateFunc" && ident.Name != "DeleteFunc" {
					continue
				}
				fn, ok := kv.Value.(*ast.FuncLit)
				if !ok {
					continue
				}
				checkBodyForGoStmts(pass, fn.Body)
			}

		case *ast.FuncDecl:
			if rehInterface == nil || !isHandlerMethod(pass, node, rehInterface) {
				return
			}
			if node.Body != nil {
				checkBodyForGoStmts(pass, node.Body)
			}
		}
	})

	return nil, nil
}

// isKnownHandlerFuncsType reports whether cl is a composite literal of
// cache.ResourceEventHandlerFuncs or cache.ResourceEventHandlerDetailedFuncs.
func isKnownHandlerFuncsType(pass *analysis.Pass, cl *ast.CompositeLit) bool {
	t := pass.TypesInfo.TypeOf(cl)
	if t == nil {
		return false
	}
	s := t.String()
	return s == "k8s.io/client-go/tools/cache.ResourceEventHandlerFuncs" ||
		s == "k8s.io/client-go/tools/cache.ResourceEventHandlerDetailedFuncs"
}

// resourceEventHandlerInterface returns the *types.Interface for
// cache.ResourceEventHandler from the analyzed package's imports, or nil.
func resourceEventHandlerInterface(pass *analysis.Pass) *types.Interface {
	for _, pkg := range pass.Pkg.Imports() {
		if pkg.Path() == "k8s.io/client-go/tools/cache" {
			obj := pkg.Scope().Lookup("ResourceEventHandler")
			if obj == nil {
				return nil
			}
			intf, ok := obj.Type().Underlying().(*types.Interface)
			if !ok {
				return nil
			}
			return intf
		}
	}
	return nil
}

// isHandlerMethod reports whether fn is an OnAdd, OnUpdate, or OnDelete method
// on a type that implements cache.ResourceEventHandler.
func isHandlerMethod(pass *analysis.Pass, fn *ast.FuncDecl, rehInterface *types.Interface) bool {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return false
	}
	name := fn.Name.Name
	if name != "OnAdd" && name != "OnUpdate" && name != "OnDelete" {
		return false
	}
	recvType := pass.TypesInfo.TypeOf(fn.Recv.List[0].Type)
	if recvType == nil {
		return false
	}
	return types.Implements(recvType, rehInterface) ||
		types.Implements(types.NewPointer(recvType), rehInterface)
}

func checkBodyForGoStmts(pass *analysis.Pass, body *ast.BlockStmt) {
	ast.Inspect(body, func(n ast.Node) bool {
		goStmt, ok := n.(*ast.GoStmt)
		if !ok {
			return true
		}
		pass.Reportf(goStmt.Pos(), "go statement in event handler dispatches work outside DSK handler tracking; queue.Add calls in the goroutine will not be observed by Flush()")
		return false
	})
}
