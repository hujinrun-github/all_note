package taskdomain

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestTaskDomainProductionCodeHasNoStorageOrLegacyDependencies(t *testing.T) {
	t.Parallel()

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() did not return architecture test path")
	}
	directory := filepath.Dir(currentFile)
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("ReadDir(%q): %v", directory, err)
	}

	forbiddenImports := []string{
		"database/sql",
		"/internal/storage",
		"/internal/repository",
		"/internal/model",
		"/internal/service",
	}
	fset := token.NewFileSet()
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("ParseFile(%q): %v", path, err)
		}
		for _, imported := range file.Imports {
			pathValue, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				t.Fatalf("unquote import %q in %s: %v", imported.Path.Value, entry.Name(), err)
			}
			for _, forbidden := range forbiddenImports {
				if pathValue == forbidden || strings.Contains(pathValue, forbidden) {
					t.Errorf("%s imports forbidden dependency %q", entry.Name(), pathValue)
				}
			}
		}

		ast.Inspect(file, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if ok && selector.Sel.Name == "Transact" {
				t.Errorf("%s calls forbidden generic Transact API at %s", entry.Name(), fset.Position(selector.Pos()))
			}
			return true
		})
	}
}

func TestTaskDomainRepositoryCapabilitiesSeparateRuntimeReadsFromFencedWrites(t *testing.T) {
	t.Parallel()

	readerType := reflect.TypeOf((*TaskDomainReader)(nil)).Elem()
	writerType := reflect.TypeOf((*TaskDomainWriter)(nil)).Elem()
	runtimeType := reflect.TypeOf((*TaskDomainReadRuntime)(nil)).Elem()
	fencedTxType := reflect.TypeOf((*TaskDomainFencedTx)(nil)).Elem()

	if readerType.NumMethod() == 0 || writerType.NumMethod() == 0 {
		t.Fatal("reader and writer repository contracts must not be empty marker interfaces")
	}
	if runtimeType.NumMethod() != 1 {
		t.Fatalf("request runtime exposes %d methods, want the read capability only", runtimeType.NumMethod())
	}
	readerMethod, ok := runtimeType.MethodByName("TaskDomainReader")
	if !ok || readerMethod.Type.NumOut() != 1 || readerMethod.Type.Out(0) != readerType {
		t.Fatalf("request runtime must expose TaskDomainReader only: %#v", readerMethod)
	}
	if _, exposed := runtimeType.MethodByName("TaskDomainWriter"); exposed {
		t.Fatal("request runtime must not expose TaskDomainWriter")
	}

	if fencedTxType.NumMethod() != 1 {
		t.Fatalf("fenced tx exposes %d methods, want the write capability only", fencedTxType.NumMethod())
	}
	writerMethod, ok := fencedTxType.MethodByName("TaskDomainWriter")
	if !ok || writerMethod.Type.NumOut() != 1 || writerMethod.Type.Out(0) != writerType {
		t.Fatalf("fenced tx must be the provider of TaskDomainWriter: %#v", writerMethod)
	}
	if _, exposed := fencedTxType.MethodByName("Transact"); exposed {
		t.Fatal("task-domain fenced tx must not expose a generic Transact escape hatch")
	}
}

func TestTaskDomainRepositorySurfaceHasNoRawStoreOrDatabaseEscapeHatch(t *testing.T) {
	t.Parallel()

	for _, contract := range []reflect.Type{
		reflect.TypeOf((*TaskDomainReader)(nil)).Elem(),
		reflect.TypeOf((*TaskDomainWriter)(nil)).Elem(),
		reflect.TypeOf((*TaskDomainReadRuntime)(nil)).Elem(),
		reflect.TypeOf((*TaskDomainFencedTx)(nil)).Elem(),
	} {
		for methodIndex := 0; methodIndex < contract.NumMethod(); methodIndex++ {
			method := contract.Method(methodIndex)
			if method.Name == "Transact" || method.Name == "Store" || method.Name == "DB" {
				t.Errorf("%s exposes forbidden method %s", contract.Name(), method.Name)
			}
			for outputIndex := 0; outputIndex < method.Type.NumOut(); outputIndex++ {
				output := method.Type.Out(outputIndex)
				if output.Name() == "Store" || output.Name() == "DB" {
					t.Errorf("%s.%s returns forbidden raw type %s", contract.Name(), method.Name, output)
				}
			}
		}
	}
}
