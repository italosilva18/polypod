package semverutil

import (
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		input   string
		want    Version
		wantErr bool
	}{
		{"1.2.3", Version{Major: 1, Minor: 2, Patch: 3}, false},
		{"v1.2.3", Version{Major: 1, Minor: 2, Patch: 3}, false},
		{"V1.2.3", Version{Major: 1, Minor: 2, Patch: 3}, false},
		{"1.2.3-alpha", Version{Major: 1, Minor: 2, Patch: 3, Pre: "alpha"}, false},
		{"1.2.3-alpha.1", Version{Major: 1, Minor: 2, Patch: 3, Pre: "alpha.1"}, false},
		{"1.2.3+build.123", Version{Major: 1, Minor: 2, Patch: 3, Build: "build.123"}, false},
		{"1.2.3-alpha+build", Version{Major: 1, Minor: 2, Patch: 3, Pre: "alpha", Build: "build"}, false},
		{"", Version{}, true},
		{"invalid", Version{}, true},
		{"1.2", Version{Major: 1, Minor: 2, Patch: 0}, false},
		{"1", Version{Major: 1, Minor: 0, Patch: 0}, false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := Parse(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("Parse(%q) = %v; want error", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q) unexpected error: %v", tt.input, err)
			}
			if got.Major != tt.want.Major || got.Minor != tt.want.Minor ||
				got.Patch != tt.want.Patch || got.Pre != tt.want.Pre ||
				got.Build != tt.want.Build {
				t.Errorf("Parse(%q) = %+v; want %+v", tt.input, got, tt.want)
			}
		})
	}
}

func TestCompare(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.0", "2.0.0", -1},
		{"2.0.0", "1.0.0", 1},
		{"1.2.3", "1.2.4", -1},
		{"1.2.3", "1.3.0", -1},
		{"1.0.0-alpha", "1.0.0", -1}, // pre-release < release
		{"1.0.0", "1.0.0-alpha", 1},
		{"1.0.0-alpha", "1.0.0-beta", -1},
		{"1.0.0-alpha.1", "1.0.0-alpha.2", -1},
		{"1.0.0-alpha.2", "1.0.0-alpha.10", -1},
		{"1.0.0-rc.1", "1.0.0", -1},
		{"1.0.0+build.1", "1.0.0+build.2", 0}, // build ignored
	}

	for _, tt := range tests {
		t.Run(tt.a+"_vs_"+tt.b, func(t *testing.T) {
			a := MustParse(tt.a)
			b := MustParse(tt.b)
			got := a.Compare(b)
			if got != tt.want {
				t.Errorf("%q.Compare(%q) = %d; want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestMatch(t *testing.T) {
	tests := []struct {
		version    string
		constraint string
		want       bool
	}{
		// Exact
		{"1.2.3", "1.2.3", true},
		{"1.2.3", "1.2.4", false},
		// Caret ^
		{"1.2.3", "^1.2.3", true},
		{"1.2.4", "^1.2.3", true},
		{"1.3.0", "^1.2.3", true},
		{"2.0.0", "^1.2.3", false},
		{"0.2.3", "^0.2.3", true},
		{"0.2.4", "^0.2.3", true},
		{"0.3.0", "^0.2.3", false},
		// Tilde ~
		{"1.2.3", "~1.2.3", true},
		{"1.2.4", "~1.2.3", true},
		{"1.3.0", "~1.2.3", false},
		// Comparison
		{"1.2.3", ">=1.0.0", true},
		{"0.9.0", ">=1.0.0", false},
		{"1.0.0", ">1.0.0", false},
		{"1.0.1", ">1.0.0", true},
		{"1.0.0", "<=1.0.0", true},
		{"1.0.1", "<=1.0.0", false},
		// Wildcard
		{"1.2.3", "*", true},
		{"1.2.3", "1.x", true},
		{"1.2.3", "2.x", false},
		{"1.2.3", "1.2.x", true},
		// AND ranges
		{"1.5.0", ">=1.0.0 <2.0.0", true},
		{"2.0.0", ">=1.0.0 <2.0.0", false},
	}

	for _, tt := range tests {
		t.Run(tt.version+"_"+tt.constraint, func(t *testing.T) {
			got, err := Match(tt.version, tt.constraint)
			if err != nil {
				t.Fatalf("Match(%q, %q) error: %v", tt.version, tt.constraint, err)
			}
			if got != tt.want {
				t.Errorf("Match(%q, %q) = %v; want %v", tt.version, tt.constraint, got, tt.want)
			}
		})
	}
}

func TestBump(t *testing.T) {
	tests := []struct {
		version string
		kind    string
		want    string
	}{
		{"1.2.3", "major", "2.0.0"},
		{"1.2.3", "minor", "1.3.0"},
		{"1.2.3", "patch", "1.2.4"},
		{"1.2.3-alpha", "patch", "1.2.4"}, // strips prerelease
		{"1.2.3+build", "patch", "1.2.4"}, // strips build
	}

	for _, tt := range tests {
		t.Run(tt.version+"_"+tt.kind, func(t *testing.T) {
			v := MustParse(tt.version)
			got := v.Bump(tt.kind).String()
			if got != tt.want {
				t.Errorf("%q.Bump(%q) = %q; want %q", tt.version, tt.kind, got, tt.want)
			}
		})
	}
}

func TestString(t *testing.T) {
	tests := []string{
		"1.2.3",
		"1.2.3-alpha",
		"1.2.3-alpha.1",
		"1.2.3+build.123",
		"1.2.3-alpha+build",
	}

	for _, s := range tests {
		t.Run(s, func(t *testing.T) {
			v := MustParse(s)
			got := v.String()
			if got != s {
				t.Errorf("String() = %q; want %q", got, s)
			}
		})
	}
}

func TestSort(t *testing.T) {
	versions := []Version{
		MustParse("1.2.3"),
		MustParse("1.0.0"),
		MustParse("2.0.0-alpha"),
		MustParse("2.0.0"),
		MustParse("1.2.3-rc.1"),
	}

	Sort(versions)

	want := []string{"1.0.0", "1.2.3-rc.1", "1.2.3", "2.0.0-alpha", "2.0.0"}
	for i, v := range versions {
		if v.String() != want[i] {
			t.Errorf("Sort[%d] = %s; want %s", i, v.String(), want[i])
		}
	}
}

func TestHighest(t *testing.T) {
	versions := []Version{
		MustParse("1.0.0"),
		MustParse("2.0.0"),
		MustParse("1.5.0"),
	}

	v, ok := Highest(versions)
	if !ok {
		t.Fatal("Highest returned not ok")
	}
	if v.String() != "2.0.0" {
		t.Errorf("Highest = %s; want 2.0.0", v.String())
	}

	v, ok = Highest(nil)
	if ok {
		t.Error("Highest(nil) should return !ok")
	}
}

func TestLess(t *testing.T) {
	a := MustParse("1.2.3")
	b := MustParse("1.2.4")
	if !a.Less(b) {
		t.Error("1.2.3.Less(1.2.4) should be true")
	}
	if b.Less(a) {
		t.Error("1.2.4.Less(1.2.3) should be false")
	}
}

func TestEqual(t *testing.T) {
	a := MustParse("1.2.3+build1")
	b := MustParse("1.2.3+build2")
	if !a.Equal(b) {
		t.Error("Equal should ignore build metadata")
	}
}

func TestIsZero(t *testing.T) {
	var v Version
	if !v.IsZero() {
		t.Error("zero Version should be IsZero")
	}

	v2 := MustParse("0.0.1")
	if v2.IsZero() {
		t.Error("0.0.1 should not be IsZero")
	}
}
