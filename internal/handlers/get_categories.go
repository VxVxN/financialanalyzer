package handlers

import "net/http"

func (controller *Controller) GetCategories(w http.ResponseWriter, r *http.Request) {
	categories, err := controller.repo.GetAllCategories()
	if err != nil {
		controller.serverError(w, "failed to list categories", err)
		return
	}
	writeJSON(w, http.StatusOK, categories)
}
