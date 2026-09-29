package handlers

import "net/http"

func (controller *Controller) GetCompanies(w http.ResponseWriter, r *http.Request) {
	companies, err := controller.repo.GetAllCompanies(r.Context())
	if err != nil {
		controller.serverError(w, "failed to list companies", err)
		return
	}
	writeJSON(w, http.StatusOK, companies)
}
