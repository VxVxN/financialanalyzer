package handlers

import "net/http"

// companyListItem is one row of the company search. Portfolio is true for a
// holding from portfolio.txt.
type companyListItem struct {
	Company   string `json:"company"`
	Category  string `json:"category"`
	Portfolio bool   `json:"portfolio,omitempty"`
}

func (controller *Controller) GetCompaniesWithCategories(w http.ResponseWriter, r *http.Request) {
	companies, err := controller.repo.GetAllCompaniesWithCategories(r.Context())
	if err != nil {
		controller.serverError(w, "failed to list companies with categories", err)
		return
	}
	out := make([]companyListItem, len(companies))
	for i, c := range companies {
		out[i] = companyListItem{Company: c.Company, Category: c.Category, Portfolio: inPortfolio(c.Company)}
	}
	writeJSON(w, http.StatusOK, out)
}
