package registry

import "testing"

// Una clave entre comillas conserva las comillas si el parser no las quita, y
// entonces el techo de un benchmark nunca se compara con el benchmark real. Ese
// bug vivió hasta que un gate en contenedor lo expuso.
func TestQuotedKeysAreUnquoted(t *testing.T) {
	doc := `
schema_version: 1
default_tier: 2
gates:
  g:
    kind: go-bench
    run: "true"
    covers: ["x/**"]
    min_inventory:
      benchmarks_max_allocs:
        "./internal/rules:BenchmarkX": 1
        'other:BenchmarkY': 0
tiers:
  1: [g]
`
	raw, err := Parse([]byte(doc), "t")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	gates := raw["gates"].(map[string]any)
	g := gates["g"].(map[string]any)
	inv := g["min_inventory"].(map[string]any)
	bm := inv["benchmarks_max_allocs"].(map[string]any)

	if _, ok := bm["./internal/rules:BenchmarkX"]; !ok {
		t.Fatalf("quoted double key kept its quotes: %v", bm)
	}
	if _, ok := bm["other:BenchmarkY"]; !ok {
		t.Fatalf("quoted single key kept its quotes: %v", bm)
	}
}
