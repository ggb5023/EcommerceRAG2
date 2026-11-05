package admin

import (
	"bytes"
	"encoding/csv"
	"reflect"
	"strings"
	"testing"
)

func TestImportTemplatesAreGeneratedWithCanonicalHeaders(t *testing.T) {
	for name, expected := range map[string]string{
		"product_table_v1": "sku_code,spu_code,title,category,brand,color,size_label,size_chart_code,capacity_ml,length_cm,width_cm,height_cm,weight_g,material,pattern,list_price_cents,currency,price_observed_at",
		"size_chart_v1":    "chart_code,size_label,size_system,chest_cm,waist_cm,hip_cm,foot_length_cm,height_cm,weight_kg,fit_note",
	} {
		filename, content, ok := renderImportTemplate(name)
		if !ok || filename != name+".csv" {
			t.Fatalf("%s filename=%q ok=%v", name, filename, ok)
		}
		if !bytes.HasPrefix(content, []byte("\ufeff")) {
			t.Fatalf("%s missing UTF-8 BOM", name)
		}
		reader := csv.NewReader(strings.NewReader(strings.TrimPrefix(string(content), "\ufeff")))
		rows, err := reader.ReadAll()
		if err != nil || len(rows) != 2 {
			t.Fatalf("%s rows=%d err=%v", name, len(rows), err)
		}
		if strings.Join(rows[0], ",") != expected {
			t.Fatalf("%s header=%q", name, strings.Join(rows[0], ","))
		}
		if len(rows[1]) != len(rows[0]) {
			t.Fatalf("%s sample columns=%d header=%d", name, len(rows[1]), len(rows[0]))
		}
	}
	if _, _, ok := renderImportTemplate("missing"); ok {
		t.Fatal("unknown template accepted")
	}
}

func TestImportTemplateProductHeaders(t *testing.T) {
	want := []string{
		"sku_code", "spu_code", "title", "category", "brand", "color",
		"size_label", "size_chart_code", "capacity_ml", "length_cm",
		"width_cm", "height_cm", "weight_g", "material", "pattern",
		"list_price_cents", "currency", "price_observed_at",
	}
	if got := importTemplateSchema["product_table_v1"].Headers; !reflect.DeepEqual(got, want) {
		t.Fatalf("product_table_v1 headers = %v, want %v", got, want)
	}
}

func TestImportTemplateSizeChartHeaders(t *testing.T) {
	want := []string{
		"chart_code", "size_label", "size_system", "chest_cm", "waist_cm",
		"hip_cm", "foot_length_cm", "height_cm", "weight_kg", "fit_note",
	}
	if got := importTemplateSchema["size_chart_v1"].Headers; !reflect.DeepEqual(got, want) {
		t.Fatalf("size_chart_v1 headers = %v, want %v", got, want)
	}
}
