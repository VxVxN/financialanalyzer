package handlers

import "net/http"

func (controller *Controller) GetCompaniesWithCategories(w http.ResponseWriter, r *http.Request) {
	companies, err := controller.repo.GetAllCompaniesWithCategories(r.Context())
	if err != nil {
		controller.serverError(w, "failed to list companies with categories", err)
		return
	}
	writeJSON(w, http.StatusOK, companies)
}
