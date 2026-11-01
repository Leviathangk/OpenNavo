package api_test

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/require"
)

const serverModulePath = "github.com/opennavo/opennavo/server"

// sizeDefault is a page size read from handler source, or why it could not be read.
type sizeDefault struct {
	value   int
	problem string
}

func nodeText(node ast.Expr) string {
	var out bytes.Buffer
	_ = printer.Fprint(&out, token.NewFileSet(), node)
	return out.String()
}

// sizeCandidate reads an expression that may hold a page size: an integer literal, a constant of an
// imported package, or a bare name. It reports why a value the test cannot see was not checked,
// instead of letting the caller skip it. ok is false for expressions that cannot be a size at all.
func sizeCandidate(expr ast.Expr, constant func(pkg, name string) (int, bool)) (sizeDefault, bool) {
	switch value := expr.(type) {
	case *ast.BasicLit:
		if value.Kind != token.INT {
			return sizeDefault{}, false
		}
		number, err := strconv.Atoi(value.Value)
		if err != nil {
			return sizeDefault{problem: nodeText(expr) + " is not an integer literal"}, true
		}
		return sizeDefault{value: number}, true
	case *ast.SelectorExpr:
		pkg, ok := value.X.(*ast.Ident)
		if !ok {
			return sizeDefault{}, false
		}
		if number, found := constant(pkg.Name, value.Sel.Name); found {
			return sizeDefault{value: number}, true
		}
		return sizeDefault{problem: nodeText(expr) + " is not an integer constant in package " + pkg.Name + ", so its value cannot be checked"}, true
	case *ast.Ident:
		return sizeDefault{problem: nodeText(expr) + " is a bare name; the contract test reads only integer literals and constants of an imported package"}, true
	}
	return sizeDefault{}, false
}

// parseSizeDefaults reads, per operationId, the page size a handler falls back to when the client
// omits ?size=, from either `fallback(request.Params.Size, N)` or `size := N` followed by an
// override from request.Params.Size. Every fallback it cannot read is reported, never skipped, so a
// new non-literal default cannot go unchecked.
func parseSizeDefaults(files []*ast.File, constant func(pkg, name string) (int, bool)) (map[string]int, []string) {
	// Handlers name the request object `request`, `r` or `req`, and some copy its params into a local
	// (`p := request.Params`), so the parser finds both by shape rather than by name.
	paramsOf := func(body *ast.BlockStmt) map[string]bool {
		requests := map[string]bool{}
		ast.Inspect(body, func(node ast.Node) bool {
			if sel, ok := node.(*ast.SelectorExpr); ok && sel.Sel.Name == "Params" {
				if ident, ok := sel.X.(*ast.Ident); ok {
					requests[ident.Name] = true
				}
			}
			return true
		})
		params := map[string]bool{}
		ast.Inspect(body, func(node ast.Node) bool {
			assign, ok := node.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for index, lhs := range assign.Lhs {
				ident, ok := lhs.(*ast.Ident)
				if !ok || index >= len(assign.Rhs) {
					continue
				}
				if sel, ok := assign.Rhs[index].(*ast.SelectorExpr); ok && sel.Sel.Name == "Params" {
					if receiver, ok := sel.X.(*ast.Ident); ok && requests[receiver.Name] {
						params[ident.Name] = true
					}
				}
			}
			return true
		})
		for name := range requests {
			params[name] = true
		}
		return params
	}
	isParams := func(expr ast.Expr, params map[string]bool) bool {
		if ident, ok := expr.(*ast.Ident); ok {
			return params[ident.Name]
		}
		sel, ok := expr.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Params" {
			return false
		}
		receiver, ok := sel.X.(*ast.Ident)
		return ok && params[receiver.Name]
	}
	isParamsSize := func(expr ast.Expr, params map[string]bool) bool {
		sel, ok := expr.(*ast.SelectorExpr)
		return ok && sel.Sel.Name == "Size" && isParams(sel.X, params)
	}
	defaults := map[string]int{}
	problems := []string{}
	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || fn.Body == nil {
				continue
			}
			operation := strings.ToLower(fn.Name.Name[:1]) + fn.Name.Name[1:]
			params := paramsOf(fn.Body)
			locals := map[string]sizeDefault{}
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				switch n := node.(type) {
				case *ast.CallExpr:
					ident, ok := n.Fun.(*ast.Ident)
					if !ok || ident.Name != "fallback" || len(n.Args) != 2 || !isParamsSize(n.Args[0], params) {
						return true
					}
					size, ok := sizeCandidate(n.Args[1], constant)
					switch {
					case !ok:
						problems = append(problems, operation+": size fallback "+nodeText(n.Args[1])+" cannot be read; use an integer literal or a constant of an imported package")
					case size.problem != "":
						problems = append(problems, operation+": size fallback "+size.problem)
					default:
						defaults[operation] = size.value
					}
				case *ast.AssignStmt:
					for index, lhs := range n.Lhs {
						ident, ok := lhs.(*ast.Ident)
						if !ok || index >= len(n.Rhs) {
							continue
						}
						if star, ok := n.Rhs[index].(*ast.StarExpr); ok && isParamsSize(star.X, params) {
							// The local keeps its earlier value as the default the client omits.
							local, seen := locals[ident.Name]
							switch {
							case !seen:
								problems = append(problems, operation+": "+ident.Name+" takes *request.Params.Size with no readable default")
							case local.problem != "":
								problems = append(problems, operation+": size default "+local.problem)
							default:
								defaults[operation] = local.value
							}
							continue
						}
						if local, ok := sizeCandidate(n.Rhs[index], constant); ok {
							locals[ident.Name] = local
						}
					}
				}
				return true
			})
		}
	}
	return defaults, problems
}

// packageFiles parses every non-test Go file in dir, so a handler that lives in its own file stays checked.
func packageFiles(t *testing.T, dir string) []*ast.File {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	fset := token.NewFileSet()
	files := []*ast.File{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		require.NoError(t, err)
		files = append(files, file)
	}
	require.NotEmpty(t, files, dir)
	return files
}

// intConstants reads the package-level integer constants declared in dir.
func intConstants(t *testing.T, dir string) map[string]int {
	t.Helper()
	values := map[string]int{}
	for _, file := range packageFiles(t, dir) {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				valueSpec, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for index, name := range valueSpec.Names {
					if index >= len(valueSpec.Values) {
						continue
					}
					literal, ok := valueSpec.Values[index].(*ast.BasicLit)
					if !ok || literal.Kind != token.INT {
						continue
					}
					if value, err := strconv.Atoi(literal.Value); err == nil {
						values[name.Name] = value
					}
				}
			}
		}
	}
	return values
}

// handlerSizeDefaults reads the public handler package and returns, per operationId, the page size
// it falls back to when the client omits ?size=, plus a problem for every fallback it could not read.
func handlerSizeDefaults(t *testing.T) (map[string]int, []string) {
	t.Helper()
	const packageDir = "../internal/http/public"
	files := packageFiles(t, packageDir)
	imports := map[string]string{}
	for _, file := range files {
		for _, spec := range file.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			require.NoError(t, err)
			name := path[strings.LastIndex(path, "/")+1:]
			if spec.Name != nil {
				name = spec.Name.Name
			}
			if strings.HasPrefix(path, serverModulePath+"/") {
				imports[name] = strings.TrimPrefix(path, serverModulePath+"/")
			}
		}
	}
	parsed := map[string]map[string]int{}
	constant := func(pkg, name string) (int, bool) {
		dir, ok := imports[pkg]
		if !ok {
			return 0, false
		}
		values, done := parsed[dir]
		if !done {
			values = intConstants(t, filepath.Join("..", dir))
			parsed[dir] = values
		}
		value, ok := values[name]
		return value, ok
	}
	return parseSizeDefaults(files, constant)
}

func specSizeDefaults(t *testing.T, path string) map[string]int {
	t.Helper()
	loader := openapi3.NewLoader()
	spec, err := loader.LoadFromFile(path)
	require.NoError(t, err)
	defaults := map[string]int{}
	for _, item := range spec.Paths.Map() {
		for _, op := range item.Operations() {
			for _, ref := range op.Parameters {
				p := ref.Value
				if p == nil || p.In != "query" || p.Name != "size" || p.Schema == nil || p.Schema.Value == nil {
					continue
				}
				if def, ok := p.Schema.Value.Default.(float64); ok {
					defaults[op.OperationID] = int(def)
				}
			}
		}
	}
	return defaults
}

// specSizeExampleMismatches reports documented ?size= examples that contradict the documented
// default. Only the default was compared with the handler before, so an example could keep
// advertising a page size the endpoint never uses.
func specSizeExampleMismatches(t *testing.T, path string) []string {
	t.Helper()
	loader := openapi3.NewLoader()
	spec, err := loader.LoadFromFile(path)
	require.NoError(t, err)
	var mismatches []string
	for _, item := range spec.Paths.Map() {
		for _, op := range item.Operations() {
			for _, ref := range op.Parameters {
				p := ref.Value
				if p == nil || p.In != "query" || p.Name != "size" || p.Schema == nil || p.Schema.Value == nil {
					continue
				}
				if p.Schema.Value.Example == nil {
					continue
				}
				def, defOK := p.Schema.Value.Default.(float64)
				example, exampleOK := p.Schema.Value.Example.(float64)
				if !defOK || !exampleOK {
					mismatches = append(mismatches, fmt.Sprintf("%s: %s documents a non-integer size default or example",
						path, op.OperationID))
					continue
				}
				if def != example {
					mismatches = append(mismatches, fmt.Sprintf("%s: %s documents size example %g but default %g",
						path, op.OperationID, example, def))
				}
			}
		}
	}
	return mismatches
}

// responsePageSize returns the page size a documented 200 response body advertises at data.size.
// A body without data (or without data.size, as in the author page, whose articles are nested one
// level deeper) has no page size of its own and reports ok as false.
func responsePageSize(example any) (float64, bool, bool) {
	body, isObject := example.(map[string]any)
	if !isObject {
		return 0, false, false
	}
	data, isObject := body["data"].(map[string]any)
	if !isObject {
		return 0, false, false
	}
	size, present := data["size"]
	if !present {
		return 0, false, false
	}
	number, isNumber := size.(float64)
	return number, true, isNumber
}

// specResponseSizeMismatches reports documented 200 responses whose data.size contradicts the
// request's documented ?size= default. Parameters alone were not enough: the response example is
// what readers copy, and a body advertising a page size the default never produces is the same
// kind of contradiction in a place the parameter check cannot see.
func specResponseSizeMismatches(t *testing.T, path string) []string {
	t.Helper()
	loader := openapi3.NewLoader()
	spec, err := loader.LoadFromFile(path)
	require.NoError(t, err)
	var mismatches []string
	withExamples, checked := 0, 0
	for route, item := range spec.Paths.Map() {
		for _, op := range item.Operations() {
			var def float64
			documented := false
			for _, ref := range op.Parameters {
				p := ref.Value
				if p == nil || p.In != "query" || p.Name != "size" || p.Schema == nil || p.Schema.Value == nil {
					continue
				}
				number, isNumber := p.Schema.Value.Default.(float64)
				if !isNumber {
					mismatches = append(mismatches, fmt.Sprintf("%s: %s documents a non-integer size default", path, op.OperationID))
					continue
				}
				def, documented = number, true
			}
			if !documented {
				continue
			}
			// The external spec documents no response bodies, so an operation may legitimately
			// have nothing to compare. When a body exists, every example in it must agree.
			examples := map[string]any{}
			if response := op.Responses.Status(200); response != nil && response.Value != nil {
				if media := response.Value.Content.Get("application/json"); media != nil {
					if media.Schema != nil && media.Schema.Value != nil {
						schema := media.Schema.Value
						if schema.Example != nil {
							examples["schema example"] = schema.Example
						}
						// A generated client reads the field example, not the whole-body one, so a
						// property left at the old page size is just as wrong and far easier to miss.
						if data, ok := schema.Properties["data"]; ok && data.Value != nil {
							if size, ok := data.Value.Properties["size"]; ok && size.Value != nil && size.Value.Example != nil {
								examples["schema data.size example"] = map[string]any{
									"data": map[string]any{"size": size.Value.Example},
								}
							}
						}
					}
					for name, example := range media.Examples {
						if example.Value == nil || example.Value.Value == nil {
							mismatches = append(mismatches, fmt.Sprintf("%s: %s example %s has no value to check", path, op.OperationID, name))
							continue
						}
						examples[name] = example.Value.Value
					}
				}
			}
			if len(examples) == 0 {
				continue
			}
			withExamples++
			for name, example := range examples {
				size, hasPage, isNumber := responsePageSize(example)
				if !hasPage {
					continue
				}
				if !isNumber {
					mismatches = append(mismatches, fmt.Sprintf("%s: %s %s has a non-integer data.size", path, op.OperationID, name))
					continue
				}
				if size != def {
					mismatches = append(mismatches, fmt.Sprintf("%s: %s (%s) %s documents data.size %g but the ?size= default is %g",
						path, route, op.OperationID, name, size, def))
				}
			}
			checked++
		}
	}
	// Deleting the response examples would make the spec unreadable rather than correct, so a
	// shrunken sweep fails here instead of passing with nothing left to compare.
	require.Equal(t, withExamples, checked, path)
	require.GreaterOrEqual(t, checked, documentedResponseBodies[path], path)
	return mismatches
}

// documentedResponseBodies is how many operations in each spec carry a documented 200 example
// with a ?size= default. It only ever moves up; lowering it is a decision, not a fix.
var documentedResponseBodies = map[string]int{
	"public.openapi.yaml":          9,
	"public-external.openapi.yaml": 0,
}

// The documented ?size= default must be what the handler really uses when the client omits it.
func TestPublicSpecSizeDefaultsMatchHandlerFallbacks(t *testing.T) {
	handler, problems := handlerSizeDefaults(t)
	require.Empty(t, problems, "every documented size fallback must be an integer literal or a resolvable package constant")
	// Guard the parser itself on the two operations whose fallback differs from the shared 24, and on
	// the two it can only read through a constant or a second file.
	require.Equal(t, 20, handler["listPackageReleases"])
	require.Equal(t, 20, handler["listCollections"])
	require.Equal(t, 500, handler["listSitemapPackages"])
	require.Equal(t, 24, handler["listBackgroundItems"])
	for _, path := range []string{"public.openapi.yaml", "public-external.openapi.yaml"} {
		spec := specSizeDefaults(t, path)
		require.NotEmpty(t, spec, path)
		for operation, documented := range spec {
			actual, ok := handler[operation]
			require.Truef(t, ok, "%s: %s documents size default %d, but the public handler package has no parsed fallback for it", path, operation, documented)
			require.Equal(t, actual, documented, "%s: %s documents size default %d, handler falls back to %d",
				path, operation, documented, actual)
		}
		// A documented example is what readers copy, so it must not contradict the default,
		// in the parameter block and in the 200 response bodies alike.
		require.Empty(t, specSizeExampleMismatches(t, path), path)
		require.Empty(t, specResponseSizeMismatches(t, path), path)
	}
}

// The parser must resolve a named constant and must report, not skip, a default it cannot see.
func TestPublicSizeDefaultParserReadsLiteralsAndConstantsAndReportsTheRest(t *testing.T) {
	const source = `package public
func (h *Handler) ListThing(ctx context.Context, request publicapi.ListThingRequestObject) (publicapi.ListThingResponseObject, error) {
	data, err := h.Catalog.Thing(requestContext(ctx), fallback(request.Params.Size, 20))
	return data, err
}
func (h *Handler) ListSitemap(ctx context.Context, request publicapi.ListSitemapRequestObject) (publicapi.ListSitemapResponseObject, error) {
	data, err := h.Catalog.Sitemap(requestContext(ctx), fallback(request.Params.Size, catalog.SitemapPageSize))
	return data, err
}
func (h *Handler) ListArticles(ctx context.Context, request publicapi.ListArticlesRequestObject) (publicapi.ListArticlesResponseObject, error) {
	size := 12
	if request.Params.Size != nil {
		size = *request.Params.Size
	}
	return h.Catalog.Articles(requestContext(ctx), size)
}
func (h *Handler) ListUnknown(ctx context.Context, request publicapi.ListUnknownRequestObject) (publicapi.ListUnknownResponseObject, error) {
	data, err := h.Catalog.Unknown(requestContext(ctx), fallback(request.Params.Size, defaultPageSize))
	return data, err
}
func (h *Handler) ListAlias(ctx context.Context, request publicapi.ListAliasRequestObject) (publicapi.ListAliasResponseObject, error) {
	size := defaultPageSize
	if request.Params.Size != nil {
		size = *request.Params.Size
	}
	return h.Catalog.Alias(requestContext(ctx), size)
}
func (h *Handler) ListComputed(ctx context.Context, request publicapi.ListComputedRequestObject) (publicapi.ListComputedResponseObject, error) {
	data, err := h.Catalog.Computed(requestContext(ctx), fallback(request.Params.Size, pageSize()))
	return data, err
}
func (h *Handler) ListAliased(ctx context.Context, request publicapi.ListAliasedRequestObject) (publicapi.ListAliasedResponseObject, error) {
	p := request.Params
	return h.Catalog.Aliased(requestContext(ctx), fallback(p.Current, 1), fallback(p.Size, 24))
}`
	file, err := parser.ParseFile(token.NewFileSet(), "handler.go", source, 0)
	require.NoError(t, err)
	constant := func(pkg, name string) (int, bool) {
		if pkg == "catalog" && name == "SitemapPageSize" {
			return 500, true
		}
		return 0, false
	}
	defaults, problems := parseSizeDefaults([]*ast.File{file}, constant)
	require.Equal(t, map[string]int{"listThing": 20, "listSitemap": 500, "listArticles": 12, "listAliased": 24}, defaults)
	require.Len(t, problems, 3, problems)
	require.Contains(t, problems[0], "listUnknown: size fallback defaultPageSize is a bare name")
	require.Contains(t, problems[1], "listAlias: size default defaultPageSize is a bare name")
	require.Contains(t, problems[2], "listComputed: size fallback pageSize() cannot be read")
}
