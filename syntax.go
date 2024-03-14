package main

import (
	"bytes"

	"github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/buildkite/terminal-to-html/v3"
)

var syntaxList = []string{"AutoHotkey", "Awk", "Bash", "C", "C++", "C#", "Clojure", "Common Lisp", "Crystal", "CSS", "D", "Dart", "Diff", "DNS", "Docker", "Elm", "EmacsLisp", "Erlang", "FSharp", "Go", "Go HTML Template", "Go Text Template", "Haskell", "HCL", "HTML", "INI", "Java", "JavaScript", "JSON", "Julia", "Kotlin", "Lua", "Makefile", "Markdown", "NASM", "Nim", "Nix", "Objective-C", "OCaml", "Org Mode", "Perl", "PHP", "PowerShell", "Prolog", "PromQL", "Protocol Buffer", "Python", "Python 2", "QBasic", "R", "Raku", "Ruby", "Rust", "SCSS", "Scala", "Scheme", "SQL", "Swift", "Tcl", "Tcsh", "Termcap", "TOML", "TypeScript", "VimL", "XML", "YAML", "Zig"}

func Pretty(code, lang string) (string, error) {
	if lang == "ansi" {
		return ANSI(code)
	}

	lexer := lexers.Get(lang)
	if lexer == nil && lang == "auto" {
		lexer = lexers.Analyse(code)
	}
	if lexer == nil {
		lexer = lexers.Fallback
	}
	// We use our own custom CSS based on this though.
	style := styles.Get("paraiso-dark")

	var w bytes.Buffer
	formatter := html.New(html.WithClasses(true), html.WithLinkableLineNumbers(true, "L"))
	iterator, err := lexer.Tokenise(nil, code)
	if err != nil {
		return "", err
	}
	err = formatter.Format(&w, style, iterator)
	if err != nil {
		return "", err
	}

	return string(w.Bytes()), nil
}

func ANSI(code string) (string, error) {
	output := terminal.Render([]byte(code))
	var b bytes.Buffer
	b.WriteString(`<pre class="chroma term-container"><code>`)
	for _, l := range bytes.Split(output, []byte{'\n'}) {
		b.WriteString(`<span class="line">`)
		b.Write(l)
		b.WriteString("\n</span>")
	}
	b.WriteString("</pre></code>")
	return string(b.Bytes()), nil
}
