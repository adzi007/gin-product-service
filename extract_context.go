package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

type ExtractedSymbol struct {
	FilePath   string
	SymbolName string
	Code       string
}

func main() {
	funcFlag := flag.String("func", "", "Target function or method name (e.g., Create)")
	fileFlag := flag.String("file", "", "Target file name or partial path (e.g., stockMoveUseCase.go)")
	dirFlag := flag.String("dir", ".", "Root directory of your Go project source code")

	flag.Parse()

	targetFunc := *funcFlag
	targetFile := *fileFlag
	rootDir := *dirFlag

	// Manual argument parsing fallback for shell quirks
	for i := 1; i < len(os.Args); i++ {
		arg := os.Args[i]
		if strings.HasPrefix(arg, "-func=") {
			targetFunc = strings.TrimPrefix(arg, "-func=")
		} else if arg == "-func" && i+1 < len(os.Args) {
			targetFunc = os.Args[i+1]
		} else if strings.HasPrefix(arg, "-file=") {
			targetFile = strings.TrimPrefix(arg, "-file=")
		} else if arg == "-file" && i+1 < len(os.Args) {
			targetFile = os.Args[i+1]
		} else if strings.HasPrefix(arg, "-dir=") {
			rootDir = strings.TrimPrefix(arg, "-dir=")
		} else if arg == "-dir" && i+1 < len(os.Args) {
			rootDir = os.Args[i+1]
		}
	}

	// Guarantee rootDir is not empty
	if strings.TrimSpace(rootDir) == "" {
		rootDir = "."
	}

	if targetFunc == "" {
		fmt.Fprintln(os.Stderr, "Error: Please specify a function name using -func")
		fmt.Fprintln(os.Stderr, "Usage: go run extract_context.go -file=stockMoveUseCase.go -func=Create -dir=.")
		os.Exit(1)
	}

	fset := token.NewFileSet()
	symbolIndex := make(map[string][]ExtractedSymbol)
	astNodes := make(map[string]ast.Node)
	var scannedFiles []string

	scriptName := filepath.Base(os.Args[0])

	// Step 1: Walk directory and parse all Go files
	err := filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		// Skip only the script file itself, not directories containing its name
		if info.Name() == "extract_context.go" || info.Name() == scriptName {
			return nil
		}

		cleanPath := filepath.ToSlash(path)
		scannedFiles = append(scannedFiles, cleanPath)

		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return nil
		}

		ast.Inspect(file, func(n ast.Node) bool {
			fn, ok := n.(*ast.FuncDecl)
			if !ok {
				return true
			}

			funcName := fn.Name.Name
			var receiverType string

			if fn.Recv != nil && len(fn.Recv.List) > 0 {
				typeExpr := fn.Recv.List[0].Type
				if star, ok := typeExpr.(*ast.StarExpr); ok {
					typeExpr = star.X
				}
				if ident, ok := typeExpr.(*ast.Ident); ok {
					receiverType = ident.Name
				}
			}

			var buf bytes.Buffer
			if err := format.Node(&buf, fset, fn); err == nil {
				sym := ExtractedSymbol{
					FilePath:   cleanPath,
					SymbolName: funcName,
					Code:       buf.String(),
				}

				if receiverType != "" {
					sym.SymbolName = receiverType + "." + funcName
				}

				symbolIndex[funcName] = append(symbolIndex[funcName], sym)
				if receiverType != "" {
					fullKey := receiverType + "." + funcName
					symbolIndex[fullKey] = append(symbolIndex[fullKey], sym)
				}

				nodeKey := sym.FilePath + ":" + sym.SymbolName
				astNodes[nodeKey] = fn
			}
			return true
		})
		return nil
	})

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error scanning project: %v\n", err)
		os.Exit(1)
	}

	// Step 2: Match entrypoint (Case-Insensitive)
	var entrySymbol *ExtractedSymbol
	candidates := symbolIndex[targetFunc]
	lowerTargetFile := strings.ToLower(targetFile)

	for _, sym := range candidates {
		if targetFile != "" {
			if strings.Contains(strings.ToLower(sym.FilePath), lowerTargetFile) {
				s := sym
				entrySymbol = &s
				break
			}
		} else {
			s := sym
			entrySymbol = &s
			break
		}
	}

	if entrySymbol == nil {
		fmt.Fprintf(os.Stderr, "Could not find function '%s'", targetFunc)
		if targetFile != "" {
			fmt.Fprintf(os.Stderr, " matching file pattern '%s'", targetFile)
		}
		fmt.Fprintln(os.Stderr, " in codebase.")
		fmt.Fprintf(os.Stderr, "\nScanned %d Go files under '%s'.\n", len(scannedFiles), rootDir)
		if len(scannedFiles) > 0 {
			fmt.Fprintln(os.Stderr, "\nSample scanned files:")
			limit := 5
			if len(scannedFiles) < limit {
				limit = len(scannedFiles)
			}
			for i := 0; i < limit; i++ {
				fmt.Fprintf(os.Stderr, " - %s\n", scannedFiles[i])
			}
		}
		os.Exit(1)
	}

	// Step 3: Extract call graph
	visited := make(map[string]bool)
	var outputBuffer bytes.Buffer

	outputBuffer.WriteString(fmt.Sprintf("# Context Extract for: `%s` in `%s`\n\n", entrySymbol.SymbolName, entrySymbol.FilePath))

	var extractCalls func(sym ExtractedSymbol)
	extractCalls = func(sym ExtractedSymbol) {
		nodeKey := sym.FilePath + ":" + sym.SymbolName
		if visited[nodeKey] {
			return
		}

		visited[nodeKey] = true
		outputBuffer.WriteString(fmt.Sprintf("### File: `%s` (%s)\n```go\n%s\n```\n\n", sym.FilePath, sym.SymbolName, sym.Code))

		node := astNodes[nodeKey]
		if node == nil {
			return
		}

		ast.Inspect(node, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}

			var calledName string
			switch expr := call.Fun.(type) {
			case *ast.Ident:
				calledName = expr.Name
			case *ast.SelectorExpr:
				calledName = expr.Sel.Name
			}

			if calledName != "" && calledName != "Error" && calledName != "Nil" {
				if targets, found := symbolIndex[calledName]; found {
					for _, targetSym := range targets {
						targetKey := targetSym.FilePath + ":" + targetSym.SymbolName
						if !visited[targetKey] {
							extractCalls(targetSym)
						}
					}
				}
			}
			return true
		})
	}

	extractCalls(*entrySymbol)
	fmt.Print(outputBuffer.String())
}
