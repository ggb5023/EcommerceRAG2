package admin

import (
	"fmt"
	"testing"
	"time"
)

func TestInspectionFingerprintStableAndScoped(t *testing.T) {
	first := inspectionFingerprint(7, inspectionKindExpired, inspectionSubjectVersion, "42")
	again := inspectionFingerprint(7, inspectionKindExpired, inspectionSubjectVersion, "42")
	if first != again {
		t.Fatalf("fingerprint changed: %q != %q", first, again)
	}
	if first == inspectionFingerprint(7, inspectionKindExpired, inspectionSubjectVersion, "43") {
		t.Fatal("different subjects share a fingerprint")
	}
	if first == inspectionFingerprint(8, inspectionKindExpired, inspectionSubjectVersion, "42") {
		t.Fatal("different tenants share a fingerprint")
	}
	if first == inspectionFingerprint(7, inspectionKindNewer, inspectionSubjectVersion, "42") {
		t.Fatal("different kinds share a fingerprint")
	}
}

func TestInspectionFindingConversions(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name   string
		count  int
		make   func(int) []inspectionFinding
		expect string
	}{
		{
			name: "expired zero", count: 0, expect: inspectionKindExpired,
			make: func(n int) []inspectionFinding {
				return expiredVersionFindings(1, "shop-a", "2026-11-06", nil)
			},
		},
		{
			name: "expired one", count: 1, expect: inspectionKindExpired,
			make: func(n int) []inspectionFinding {
				rows := make([]expiredVersionRow, n)
				for i := range rows {
					rows[i] = expiredVersionRow{DocumentID: int64(i + 1), LogicalKey: fmt.Sprintf("doc-%d", i),
						Title: "Doc", VersionID: fmt.Sprintf("%d", i+1), EffectiveTo: "2026-11-05"}
				}
				return expiredVersionFindings(1, "shop-a", "2026-11-06", rows)
			},
		},
		{
			name: "expired many", count: 3, expect: inspectionKindExpired,
			make: func(n int) []inspectionFinding {
				rows := make([]expiredVersionRow, n)
				for i := range rows {
					rows[i] = expiredVersionRow{DocumentID: int64(i + 1), LogicalKey: fmt.Sprintf("doc-%d", i),
						Title: "Doc", VersionID: fmt.Sprintf("%d", i+1), EffectiveTo: "2026-11-05"}
				}
				return expiredVersionFindings(1, "shop-a", "2026-11-06", rows)
			},
		},
		{
			name: "newer zero", count: 0, expect: inspectionKindNewer,
			make: func(int) []inspectionFinding { return newerVersionFindings(1, "shop-a", nil) },
		},
		{
			name: "newer one", count: 1, expect: inspectionKindNewer,
			make: func(n int) []inspectionFinding {
				rows := make([]newerVersionConflictRow, n)
				for i := range rows {
					rows[i] = newerVersionConflictRow{DocumentID: int64(i + 1), LogicalKey: fmt.Sprintf("doc-%d", i),
						Title: "Doc", VersionID: "1", NewVersionIDs: []string{"2"}, VersionCreated: now}
				}
				return newerVersionFindings(1, "shop-a", rows)
			},
		},
		{
			name: "newer many", count: 2, expect: inspectionKindNewer,
			make: func(n int) []inspectionFinding {
				rows := make([]newerVersionConflictRow, n)
				for i := range rows {
					rows[i] = newerVersionConflictRow{DocumentID: int64(i + 1), LogicalKey: fmt.Sprintf("doc-%d", i),
						Title: "Doc", VersionID: "1", NewVersionIDs: []string{"2"}, VersionCreated: now}
				}
				return newerVersionFindings(1, "shop-a", rows)
			},
		},
		{
			name: "gaps zero", count: 0, expect: inspectionKindUnanswered,
			make: func(int) []inspectionFinding { return gapFindings(1, "shop-a", nil) },
		},
		{
			name: "gaps one", count: 1, expect: inspectionKindUnanswered,
			make: func(n int) []inspectionFinding {
				gaps := make([]KnowledgeGap, n)
				for i := range gaps {
					gaps[i] = KnowledgeGap{Query: "退款", Normalized: "退款", Count: int64(INSPECTION_MIN_CLUSTER_TURNS), LastSeen: now, Reason: "failed"}
				}
				return gapFindings(1, "shop-a", gaps)
			},
		},
		{
			name: "gaps many", count: 2, expect: inspectionKindUnanswered,
			make: func(n int) []inspectionFinding {
				gaps := make([]KnowledgeGap, n)
				for i := range gaps {
					gaps[i] = KnowledgeGap{Query: fmt.Sprintf("问题%d", i), Normalized: fmt.Sprintf("问题%d", i), Count: int64(INSPECTION_MIN_CLUSTER_TURNS), LastSeen: now, Reason: "failed"}
				}
				return gapFindings(1, "shop-a", gaps)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			items := tc.make(tc.count)
			if len(items) != tc.count {
				t.Fatalf("findings=%d want %d", len(items), tc.count)
			}
			for _, item := range items {
				if item.Kind != tc.expect {
					t.Fatalf("kind=%q want %q", item.Kind, tc.expect)
				}
				if item.Fingerprint == "" || item.SubjectID == "" || item.Evidence == nil {
					t.Fatalf("incomplete finding: %+v", item)
				}
			}
		})
	}
}

func TestInspectionEvidenceExcludesAnswerText(t *testing.T) {
	finding := gapFindings(1, "shop-a", []KnowledgeGap{{
		Query: "退款要多久？", Normalized: "退款要多久", Count: 2, LastSeen: time.Now(), Reason: "failed",
	}})[0]
	if _, ok := finding.Evidence["answer"]; ok {
		t.Fatal("gap evidence must not carry an answer body")
	}
	if _, ok := finding.Evidence["content"]; ok {
		t.Fatal("gap evidence must not carry customer conversation content")
	}
}
