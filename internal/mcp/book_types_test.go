package mcp

import (
	"context"
	"slices"
	"strings"
	"testing"

	"dalibre/internal/calibre"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestBookTypeTools(t *testing.T) {
	lib := testLibrary(t)
	books, err := lib.Books()
	if err != nil {
		t.Fatal(err)
	}
	leftHand := books[slices.IndexFunc(books, func(b *calibre.Book) bool { return b.Title == "The Left Hand of Darkness" })]
	earthsea := books[slices.IndexFunc(books, func(b *calibre.Book) bool { return b.Title == "A Wizard of Earthsea" })]
	custom, err := calibre.NewCustomBookType("思想随笔")
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.UpdateBookTypes(leftHand, calibre.BookTypeEdit{IDs: []string{custom.ID}, Create: []calibre.BookType{custom}}); err != nil {
		t.Fatal(err)
	}
	s := New(&testHost{lib: lib}, "test", true)
	cs := connect(t, s)

	var types bookTypesOut
	if msg := call(t, cs, "list_book_types", map[string]any{}, &types); msg != "" {
		t.Fatal(msg)
	}
	if !slices.ContainsFunc(types.BookTypes, func(typ calibre.BookType) bool {
		return typ.ID == custom.ID && typ.Name == "思想随笔" && typ.Custom
	}) || !slices.ContainsFunc(types.BookTypes, func(typ calibre.BookType) bool {
		return typ.ID == "scienceFiction" && typ.Name != "" && !typ.Custom
	}) {
		t.Fatalf("types: %+v", types)
	}

	search := func(args map[string]any) searchOut {
		t.Helper()
		var out searchOut
		if msg := call(t, cs, "search_books", args, &out); msg != "" {
			t.Fatal(msg)
		}
		return out
	}
	if out := search(map[string]any{"author": "Ursula K. Le Guin", "uncategorized": true}); out.Total != 1 || out.Books[0].ID != earthsea.ID || out.Books[0].BookTypeIDs == nil || len(out.Books[0].BookTypeIDs) != 0 {
		t.Fatalf("uncategorized search: %+v", out)
	}
	if out := search(map[string]any{"book_type_id": "scienceFiction"}); out.Total != 0 {
		t.Fatalf("unused type search: %+v", out)
	}
	for _, args := range []map[string]any{
		{"book_type_id": "invented"},
		{"book_type_id": custom.ID, "uncategorized": true},
	} {
		if msg := call(t, cs, "search_books", args, nil); msg == "" {
			t.Fatalf("accepted invalid search arguments: %+v", args)
		}
	}

	update := func(ids []int64, fields map[string]any) updateOut {
		t.Helper()
		var out updateOut
		if msg := call(t, cs, "update_books", map[string]any{"ids": ids, "patch": fields}, &out); msg != "" {
			t.Fatal(msg)
		}
		return out
	}
	mark := map[string]any{"add_book_type_ids": []string{"fiction", "scienceFiction", "fiction"}}
	out := update([]int64{leftHand.ID, earthsea.ID, 999}, mark)
	if len(out.Updated) != 2 || len(out.Failed) != 1 || out.Failed[0].ID != 999 {
		t.Fatalf("partial batch: %+v", out)
	}
	out = update([]int64{leftHand.ID}, mark)
	if len(out.Updated) != 1 || len(out.Failed) != 0 || len(out.Updated[0].BookTypeIDs) != 3 || !slices.Contains(out.Updated[0].BookTypeIDs, custom.ID) || !slices.Equal(out.Updated[0].Tags, leftHand.Tags) {
		t.Fatalf("repeated add: %+v", out)
	}
	previousTypes := out.Updated[0].BookTypeIDs
	out = update([]int64{leftHand.ID}, map[string]any{"add_book_type_ids": []string{}})
	if len(out.Updated) != 1 || len(out.Failed) != 0 || !slices.Equal(out.Updated[0].BookTypeIDs, previousTypes) {
		t.Fatalf("empty add changed types: %+v", out)
	}
	if out := search(map[string]any{"query": "earthsea", "book_type_id": "scienceFiction", "language": "eng"}); out.Total != 1 || out.Books[0].ID != earthsea.ID {
		t.Fatalf("type with other search criteria: %+v", out)
	}
	if out := search(map[string]any{"book_type_id": custom.ID}); out.Total != 1 || out.Books[0].ID != leftHand.ID {
		t.Fatalf("custom type search: %+v", out)
	}

	out = update([]int64{earthsea.ID}, map[string]any{"add_book_type_ids": []string{"invented", "history"}, "add_tags": []string{"Should not be saved"}})
	if len(out.Updated) != 0 || len(out.Failed) != 1 || out.Failed[0].ID != earthsea.ID || !strings.Contains(out.Failed[0].Error, "invented") {
		t.Fatalf("unknown type: %+v", out)
	}
	var b Book
	if msg := call(t, cs, "get_book", map[string]any{"id": earthsea.ID}, &b); msg != "" || len(b.BookTypeIDs) != 2 || slices.Contains(b.BookTypeIDs, "history") || !slices.Equal(b.Tags, earthsea.Tags) {
		t.Fatalf("failed mixed update had side effects: %+v, %s", b, msg)
	}
	out = update([]int64{earthsea.ID}, map[string]any{"add_book_type_ids": []string{"literature"}, "add_tags": []string{"Favorite"}})
	if len(out.Updated) != 1 || len(out.Failed) != 0 || !slices.Contains(out.Updated[0].BookTypeIDs, "literature") || !slices.Contains(out.Updated[0].Tags, "Favorite") {
		t.Fatalf("mixed update: %+v", out)
	}
	assigned := out.Updated[0].BookTypeIDs
	out = update([]int64{earthsea.ID}, map[string]any{"remove_tags": []string{"Favorite"}})
	if len(out.Updated) != 1 || len(out.Failed) != 0 || !slices.Equal(out.Updated[0].BookTypeIDs, assigned) {
		t.Fatalf("tag-only update changed types: %+v", out)
	}
	s.SetAllowChanges(false)
	if names := toolNames(t, cs); !slices.Contains(names, "list_book_types") || slices.Contains(names, "update_books") {
		t.Fatalf("read-only tools: %v", names)
	}
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "update_books", Arguments: map[string]any{
		"ids": []int64{earthsea.ID}, "patch": map[string]any{"add_book_type_ids": []string{"history"}},
	}})
	if err == nil && !res.IsError {
		t.Fatal("book type write succeeded with read-only MCP")
	}
}
