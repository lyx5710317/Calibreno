package plugin

import (
	"net/http"
	"testing"
)

func TestMarketplaceRepository(t *testing.T) {
	for _, repo := range []string{"lyx5710317/Calibreno", "lyx5710317/calibreno", "djyde/dalibre"} {
		if !IsMarketplaceRepo(repo) {
			t.Errorf("marketplace repository %q was not recognized", repo)
		}
	}
	if IsMarketplaceRepo("another/Calibreno") {
		t.Fatal("an unrelated repository was recognized")
	}
}

func TestParseMarketplaceOmitsBookSources(t *testing.T) {
	index, err := ParseMarketplace([]byte(`{
		"categories":[{"id":"sources","title":"Book sources"},{"id":"metadata","title":"Metadata"}],
		"plugins":[
			{"id":"example.books","name":"Books","version":"1.0.0","category":"metadata","package":"https://example.com/books",
			 "capabilities":{"bookSource":{},"metadata":{}}},
			{"id":"example.legacy","name":"Legacy source","version":"1.0.0","package":"https://example.com/legacy",
			 "capabilities":["source"]},
			{"id":"example.metadata","name":"Metadata","version":"1.0.0","package":"https://example.com/metadata",
			 "capabilities":{"metadata":{}}}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(index.Plugins) != 1 || index.Plugins[0].ID != "example.metadata" ||
		len(index.Categories) != 1 || index.Categories[0].ID != "metadata" {
		t.Fatalf("legacy download sources reached the market: %+v", index)
	}
}

func TestFetchMarketplaceCatalogURL(t *testing.T) {
	const primary = "https://raw.githubusercontent.com/lyx5710317/Calibreno/refs/heads/marketplace/marketplace.json"
	const custom = "https://example.com/marketplace.json"
	var requested string
	gh := &GitHub{Client: &http.Client{Transport: serve{h: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = r.URL.String()
		_, _ = w.Write([]byte(`{"name":"Catalog","plugins":[]}`))
	})}}}
	for _, tt := range []struct{ input, want string }{{"", primary}, {custom, custom}} {
		index, _, err := gh.FetchMarketplace(t.Context(), tt.input)
		if err != nil || index.Name != "Catalog" || requested != tt.want {
			t.Fatalf("fetch %q from %q: index=%v error=%v", tt.want, requested, index, err)
		}
	}
}
