package standards

import (
	"slices"
	"strings"
	"testing"
)

func TestLanguageProbesCountDeclarationsAndExcludeLiteralText(t *testing.T) {
	for _, tc := range []struct {
		id, path, src string
		good, total   int
	}{
		{"python-function-snake-case", "app.py", "# def BadComment(): pass\ntext = '''def BadString(): pass'''\nasync def fetch_user(): pass\ndef BadName(): pass\ndef __init__(self): pass\n", 2, 3},
		{"python-class-pascal-case", "app.pyi", "# class bad_comment: pass\ntext = \"class bad_string: pass\"\nclass Client: pass\nclass bad_name: pass\nclass HTTPClient: pass\n", 2, 3},
		{"javascript-class-pascal-case", "app.mjs", "// class bad_comment {}\nconst text = `class bad_string {}`;\nclass Client {}\nclass bad_name {}\nclass HTTPClient {}\n", 2, 3},
		{"javascript-strict-equality", "app.cjs", "// a == b\nconst text = 'a != b';\nconst regex = /a==b/;\na === b;\na == b;\na !== b;\n", 2, 3},
		{"java-type-pascal-case", "App.java", "// class bad_comment {}\nclass Client { String s = \"interface bad_string {}\"; }\ninterface bad_name {}\nenum Status { OK }\nrecord User(int id) {}\n", 3, 4},
		{"java-package-lowercase", "App.java", "/* package Bad.Comment; */\npackage com.Example.app;\nimport Good.Import;\nclass App {}\n", 0, 1},
		{"ruby-method-snake-case", "app.rb", "# def BadComment; end\ntext = %q{def BadString; end}\ndef fetch_user; end\ndef self.BadName; end\ndef ready?; end\ndef []=(key, value); end\n", 2, 3},
		{"ruby-type-pascal-case", "tasks.rake", "# class Bad_Comment; end\ntext = 'module Bad_String; end'\nmodule Tools; end\nclass Bad_Name; end\nclass HTTPClient; end\nclass << self; end\n", 2, 3},
	} {
		t.Run(tc.id, func(t *testing.T) {
			got := measureOne(t, tc.id, tc.path, tc.src)
			want(t, got, tc.good, tc.total)
			if len(got.Off) != tc.total-tc.good {
				t.Fatalf("off=%v", got.Off)
			}
			for _, off := range got.Off {
				if off.Path != tc.path || off.Line < 1 || off.Excerpt != strings.TrimSpace(strings.Split(tc.src, "\n")[off.Line-1]) {
					t.Errorf("wrong location: %+v", off)
				}
			}
		})
	}
}

func TestLanguageProbesKeepEmptyInputsUnseen(t *testing.T) {
	for _, lang := range []string{"python", "javascript", "java", "ruby"} {
		if !slices.Contains(Languages(), lang) {
			t.Errorf("%s is not supported", lang)
		}
	}
	for _, tc := range []struct{ path, src string }{
		{"empty.py", "# def Fake(): pass\n\"\"\"class fake: pass\"\"\"\n"},
		{"empty.js", "/* class fake {} */\nconst text = `a == b`;\n"},
		{"Empty.java", "// package Bad.Name;\n/* class fake {} */\n"},
		{"empty.rb", "=begin\ndef Fake; end\n=end\ntext = <<~DOC\nclass Fake_Name; end\nDOC\n"},
	} {
		rep := Measure([]File{{Path: tc.path, Src: []byte(tc.src)}}, Options{})
		if len(rep.Unmeasured) != 0 {
			t.Fatalf("clean literal failed lexing: %v", rep.Unmeasured)
		}
		for _, r := range rep.Results {
			if r.Total != 0 || r.Standing != StandingUnseen {
				t.Errorf("%s: %s counted non-code: %+v", tc.path, r.ID, r)
			}
		}
	}
}

func TestLanguageStandardsScoreOnlyChangedLinesAgainstTheBase(t *testing.T) {
	for _, tc := range []struct{ id, path, good, bad string }{
		{"python-function-snake-case", "app.py", "def fetch_user(): pass\n", "def FetchUser(): pass\n"},
		{"javascript-strict-equality", "app.js", "a === b;\n", "a == b;\n"},
		{"java-package-lowercase", "App.java", "package com.example;\n", "package com.Example;\n"},
		{"ruby-method-snake-case", "app.rb", "def fetch_user; end\n", "def FetchUser; end\n"},
	} {
		opts := Options{Floor: Floor{MinSites: 1, MinShare: 0.85}}
		base := Measure([]File{{Path: tc.path, Src: []byte(tc.good)}}, opts)
		head := []File{{Path: tc.path, Src: []byte(tc.bad)}}
		score := Score(head, base, TouchedLines(map[string][]int{tc.path: {1}}), opts)[tc.id]
		if score.Total != 1 || score.Conforming != 0 || len(score.Off) != 1 {
			t.Errorf("%s: %+v", tc.id, score)
		}
		if got := Score(head, base, TouchedLines(map[string][]int{tc.path: {2}}), opts); len(got) != 0 {
			t.Errorf("untouched violation scored: %v", got)
		}
	}
}

func TestLanguageProbesHandleNestedNamesAndModernLiterals(t *testing.T) {
	for _, tc := range []struct {
		id, path, src string
		good, total   int
	}{
		{"ruby-type-pascal-case", "app.rb", "class Admin::Bad_Name; end\nmodule Good::Tools; end\n", 1, 2},
		{"ruby-method-snake-case", "app.rb", "def self.ready?; end\ndef value=(x); end\ndef []=(k,v); end\ndef BadName!; end\n", 2, 3},
		{"python-class-pascal-case", "app.py", "class _Private: pass\nclass Bad_Name: pass\ns = f'''class Not_A_Class: {1}'''\n", 1, 2},
		{"javascript-strict-equality", "view.jsx", "const element = <p title='a == b'>a != b {a === b}</p>;\nconst text = `literal == text ${a != b}`;\n", 1, 2},
		{"javascript-class-pascal-case", "view.jsx", "const element = <p>class fake_type {}</p>;\nclass RealType {}\n", 1, 1},
		{"java-type-pascal-case", "App.java", "@interface Bad_Name {}\nclass App { String s = \"\"\"\nclass Not_A_Class {}\n\"\"\"; }\n", 1, 2},
		{"ruby-type-pascal-case", "app.rb", "text = <<'DOC'\nclass Fake_Name; end\nDOC\nclass RealType; end\n", 1, 1},
		{"ruby-type-pascal-case", "app.rb", "text = <<~DOC\nclass Fake_Name; end\n  DOC\nclass RealType; end\n", 1, 1},
	} {
		t.Run(tc.id+tc.src, func(t *testing.T) { want(t, measureOne(t, tc.id, tc.path, tc.src), tc.good, tc.total) })
	}
}

func TestRubyQualifiedTypesReportTheViolatingDeclaration(t *testing.T) {
	got := measureOne(t, "ruby-type-pascal-case", "app.rb", "class Admin::Bad_Name; end\nmodule Good::Tools; end\n")
	want(t, got, 1, 2)
	if len(got.Off) != 1 || got.Off[0].Line != 1 {
		t.Fatalf("wrong declaration: %+v", got.Off)
	}
}

func TestUnsupportedLiteralFormsAreReportedRatherThanCountedAsCode(t *testing.T) {
	for _, tc := range []struct{ path, src string }{
		{"app.rb", "text = <<~DOC\nclass Fake_Name; end\n"},
		{"App.java", `// \u000a class Hidden_Name {}`},
		{"app.rb", "x = <<A, <<B\nclass Fake_A; end\nA\nclass Fake_B; end\nB\n"},
	} {
		rep := Measure([]File{{Path: tc.path, Src: []byte(tc.src)}}, Options{})
		if len(rep.Unmeasured) != 1 {
			t.Errorf("%s: missing coverage warning: %+v", tc.path, rep)
		}
		for _, r := range rep.Results {
			if r.Total != 0 {
				t.Errorf("partial lexer counted sites: %+v", r)
			}
		}
	}
}

func TestJavaScriptLessThanExpressionsAreNotJSXTags(t *testing.T) {
	for _, src := range []string{
		"if (a<b) { x == y; }\n",
		"const smaller = a<b; x == y;\n",
		"const view = <><p>a == b</p><span>{x == y}</span></>;\n",
		"function view() { return <p>{x == y}</p>; }\n",
		"const view = rows.map(x => <p enabled={x === 1}>x != y</p>);\n",
	} {
		got := measureOne(t, "javascript-strict-equality", "view.jsx", src)
		if got.Total != 1 {
			t.Errorf("source %q: %+v", src, got)
		}
	}
}

func TestRegexStatementsAfterConditionsDoNotContributeDeclarationSites(t *testing.T) {
	src := "if ((ready)) /class fake_name == other/.test(text);\nclass RealName {}\n"
	want(t, measureOne(t, "javascript-class-pascal-case", "app.js", src), 1, 1)
	want(t, measureOne(t, "javascript-strict-equality", "app.js", src), 0, 0)
}

func TestRubyRootQualifiedDeclarationsIncludeTheirFinalName(t *testing.T) {
	got := measureOne(t, "ruby-type-pascal-case", "app.rb", "class ::Root::Bad_Name; end\nmodule ::Root::Good; end\n")
	want(t, got, 1, 2)
	if len(got.Off) != 1 || got.Off[0].Line != 1 {
		t.Fatalf("wrong declaration: %+v", got.Off)
	}
}
