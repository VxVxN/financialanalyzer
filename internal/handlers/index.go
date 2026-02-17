package handlers

import (
	"html/template"
	"net/http"
)

func (controller *Controller) IndexHandler(w http.ResponseWriter, r *http.Request) {
	tmpl, err := template.ParseFiles("templates/index.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	metrics := []string{"revenue", "net_profit", "ebitda", "pe", "roe", "capitalization", "debt"}

	data := struct {
		Metrics []string
	}{
		Metrics: metrics,
	}

	w.Header().Set("Content-Type", "text/html")
	tmpl.Execute(w, data)
}
