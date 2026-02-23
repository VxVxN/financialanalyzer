package handlers

import (
	"encoding/json"
	"net/http"
)

func (controller *Controller) GetCategories(w http.ResponseWriter, r *http.Request) {
	categories, err := controller.repo.GetAllCategories()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(categories)
}
