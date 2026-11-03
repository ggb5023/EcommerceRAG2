package admin

import (
	"bytes"
	"encoding/csv"
	"net/http"
)

// importTemplateSchema is the single Go-side description used to render the
// downloadable CSV headers. It mirrors catalog.py's canonical columns; the
// generated CSV (not a committed template file) is the user-facing artifact.
var importTemplateSchema = map[string]struct {
	Filename string
	Headers  []string
	Sample   []string
}{
	"product_table_v1": {
		Filename: "product_table_v1.csv",
		Headers: []string{
			"sku_code", "spu_code", "title", "category", "brand", "color",
			"size_label", "size_chart_code", "capacity_ml", "length_cm",
			"width_cm", "height_cm", "weight_g", "material", "pattern",
			"list_price_cents", "currency", "price_observed_at",
		},
		Sample: []string{
			"SKU-EXAMPLE", "SPU-EXAMPLE", "示例商品", "示例类目", "示例品牌", "黑色",
			"M", "CHART-EXAMPLE", "480", "10", "8", "20", "300", "示例材质", "纯色",
			"8900", "CNY", "2026-01-01",
		},
	},
	"size_chart_v1": {
		Filename: "size_chart_v1.csv",
		Headers: []string{
			"chart_code", "size_label", "size_system", "chest_cm", "waist_cm",
			"hip_cm", "foot_length_cm", "height_cm", "weight_kg", "fit_note",
		},
		Sample: []string{
			"CHART-EXAMPLE", "M", "unisex", "90", "72", "96", "", "170", "60", "示例版型说明",
		},
	},
}

func renderImportTemplate(name string) (string, []byte, bool) {
	spec, ok := importTemplateSchema[name]
	if !ok {
		return "", nil, false
	}
	var buffer bytes.Buffer
	buffer.WriteString("\ufeff")
	writer := csv.NewWriter(&buffer)
	_ = writer.Write(spec.Headers)
	_ = writer.Write(spec.Sample)
	writer.Flush()
	return spec.Filename, buffer.Bytes(), writer.Error() == nil
}

func (g *Handler) adminImportTemplate(w http.ResponseWriter, r *http.Request) {
	if _, ok := g.requireAdmin(w, r, adminRoleMerchant); !ok {
		return
	}
	filename, content, ok := renderImportTemplate(r.PathValue("name"))
	if !ok {
		http.Error(w, "template not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}
