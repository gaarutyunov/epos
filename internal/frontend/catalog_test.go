package frontend

import "testing"

func TestFilterByKeyword(t *testing.T) {
	cat := NewCatalog([]SkillCard{
		{Name: "pdf-tools", Description: "PDF extraction", Registry: "gitlab"},
		{Name: "csv-tools", Description: "CSV wrangling", Registry: "gitlab"},
		{Name: "img-tools", Description: "image ops", Registry: "gitlab"},
	})
	got := cat.Filter(Filter{Keyword: "csv"})
	if len(got) != 1 || got[0].Name != "csv-tools" {
		t.Fatalf("filter by 'csv' = %+v, want only csv-tools", got)
	}
}

func TestFilterEmptyReturnsAll(t *testing.T) {
	cat := NewCatalog([]SkillCard{{Name: "a"}, {Name: "b"}})
	if len(cat.Filter(Filter{})) != 2 {
		t.Error("empty filter should return all cards")
	}
}
