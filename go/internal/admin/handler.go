package admin

import (
	"net/http"
)

func (g *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/v1/session", g.adminSession)
	mux.HandleFunc("GET /admin/v1/overview", g.adminOverview)
	mux.HandleFunc("GET /admin/v1/access/accounts", g.adminAccounts)
	mux.HandleFunc("GET /admin/v1/access/requests", g.adminListRequests)
	mux.HandleFunc("POST /admin/v1/access/requests", g.adminCreateRequest)
	mux.HandleFunc("POST /admin/v1/access/requests/{id}/decision", g.adminDecideRequest)
	mux.HandleFunc("POST /admin/v1/staff", g.adminCreateStaff)
	mux.HandleFunc("GET /admin/v1/access/audit", g.adminAudit)
	mux.HandleFunc("POST /admin/v1/access/grants/{id}/revoke", g.adminRevokeGrant)
	mux.HandleFunc("GET /admin/v1/merchant/shops", g.adminShops)
	mux.HandleFunc("POST /admin/v1/merchant/shops/{id}/settings", g.adminShopSettings)
	mux.HandleFunc("GET /admin/v1/merchant/sources", g.adminSources)
	mux.HandleFunc("POST /admin/v1/merchant/sources/import", g.adminImportPackage)
	mux.HandleFunc("POST /admin/v1/merchant/imports", g.adminCreateImport)
	mux.HandleFunc("GET /admin/v1/merchant/agent-review", g.adminAgentReviewSetting)
	mux.HandleFunc("PUT /admin/v1/merchant/agent-review", g.adminAgentReviewSetting)
	mux.HandleFunc("GET /admin/v1/merchant/imports/{id}", g.adminImportPreview)
	mux.HandleFunc("POST /admin/v1/merchant/imports/{id}/confirm", g.adminConfirmImport)
	mux.HandleFunc("POST /admin/v1/merchant/imports/{id}/revoke", g.adminRevokeImport)
	mux.HandleFunc("GET /admin/v1/merchant/ingestion", g.adminJobs)
	mux.HandleFunc("GET /admin/v1/merchant/ingestion/{id}", g.adminJobDetail)
	mux.HandleFunc("POST /admin/v1/merchant/ingestion/{id}/cancel", g.adminCancelIngestJob)
	mux.HandleFunc("POST /admin/v1/merchant/ingestion/{id}/retry", g.adminRetryIngestJob)
	mux.HandleFunc("GET /admin/v1/merchant/reviews", g.adminReviews)
	mux.HandleFunc("GET /admin/v1/merchant/reviews/{id}", g.adminReviewDetail)
	mux.HandleFunc("POST /admin/v1/merchant/versions/{id}/review", g.adminReviewVersion)
	mux.HandleFunc("POST /admin/v1/merchant/versions/{id}/publish", g.adminPublishVersion)
	mux.HandleFunc("POST /admin/v1/merchant/versions/{id}/rollback", g.adminRollbackVersion)
	mux.HandleFunc("POST /admin/v1/merchant/versions/{id}/revoke", g.adminRevokeVersion)
	mux.HandleFunc("GET /admin/v1/merchant/versions", g.adminVersions)
	mux.HandleFunc("GET /admin/v1/merchant/commands/{id}", g.adminCommandResult)
}
