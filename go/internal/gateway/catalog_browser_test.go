package gateway

import "testing"

func TestParseCatalogBrowseFilter(t *testing.T) {
	cases := []struct {
		name   string
		values map[string][]string
		ok     bool
		page   int
		size   int
	}{
		{"defaults", map[string][]string{}, true, 1, 24},
		{"paging", map[string][]string{"page": {"3"}, "page_size": {"10"}}, true, 3, 10},
		{"filters", map[string][]string{"q": {"保温杯"}, "category": {"日用品"}, "sku": {"SKU-1"}, "attribute_key": {"capacity_ml"}, "attribute_value": {"480"}}, true, 1, 24},
		{"bad page", map[string][]string{"page": {"0"}}, false, 0, 0},
		{"bad size", map[string][]string{"page_size": {"101"}}, false, 0, 0},
		{"attribute without value", map[string][]string{"attribute_key": {"capacity_ml"}}, false, 0, 0},
		{"value without attribute", map[string][]string{"attribute_value": {"480"}}, false, 0, 0},
		{"unknown attribute", map[string][]string{"attribute_key": {"secret"}, "attribute_value": {"x"}}, false, 0, 0},
		{"query too long", map[string][]string{"q": {string(make([]rune, catalogBrowseMaxQuery+1))}}, false, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			filter, ok := parseCatalogBrowseFilter(tc.values)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if ok && (filter.Page != tc.page || filter.PageSize != tc.size) {
				t.Fatalf("page/size = %d/%d, want %d/%d", filter.Page, filter.PageSize, tc.page, tc.size)
			}
		})
	}
}
