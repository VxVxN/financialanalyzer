package handlers

import (
	"fmt"
	"html"
	"net/http"
	"strconv"

	"github.com/VxVxN/financialanalyzer/internal/models"
)

// manualField is one figure of the manual-entry form, keyed by its JSON name.
type manualField struct {
	key, label string
	get        func(models.ManualFinancials) *float64
}

var manualFields = []manualField{
	{"revenue", "Выручка", func(m models.ManualFinancials) *float64 { return m.Revenue }},
	{"net_profit", "Чистая прибыль", func(m models.ManualFinancials) *float64 { return m.NetProfit }},
	{"ebitda", "EBITDA", func(m models.ManualFinancials) *float64 { return m.EBITDA }},
	{"operating_profit", "Операционная прибыль", func(m models.ManualFinancials) *float64 { return m.OperatingProfit }},
	{"operating_cash_flow", "Операционный денежный поток", func(m models.ManualFinancials) *float64 { return m.OperatingCashFlow }},
	{"capex", "Капзатраты", func(m models.ManualFinancials) *float64 { return m.Capex }},
	{"debt", "Долг", func(m models.ManualFinancials) *float64 { return m.Debt }},
	{"cash", "Денежные средства", func(m models.ManualFinancials) *float64 { return m.Cash }},
	{"equity", "Капитал", func(m models.ManualFinancials) *float64 { return m.Equity }},
	{"dividends", "Дивиденды за год", func(m models.ManualFinancials) *float64 { return m.Dividends }},
}

const manualCSS = `
.manual-card { padding: 20px; }
.manual-card p.hint { color: var(--text-2); font-size: 13px; margin: 0 0 16px; line-height: 1.6; max-width: 820px; }
.manual-card .table-wrap { margin-bottom: 16px; border: 1px solid var(--border); border-radius: var(--radius); }
.manual-card td.actions { white-space: nowrap; }
.manual-card td.actions button { margin-left: 4px; }
.manual-form { display: grid; grid-template-columns: repeat(auto-fill, minmax(180px, 1fr)); gap: 12px 16px; }
.manual-form label { display: flex; flex-direction: column; gap: 6px; font-size: 12px; color: var(--text-3); font-weight: 500; }
.manual-form input { min-height: 40px; padding: 8px 10px; background: var(--surface); color: var(--text-1); border: 1px solid var(--border-strong); border-radius: var(--radius-sm); font: inherit; font-size: 14px; font-variant-numeric: tabular-nums; }
.manual-form input:focus { outline: none; border-color: var(--accent); box-shadow: var(--focus); }
.manual-actions { display: flex; gap: 10px; align-items: center; margin-top: 16px; flex-wrap: wrap; }
.manual-card .status { font-size: 13px; color: var(--text-3); }
`

// fmtManual renders a stored figure for the entries table ("—" when not entered).
func fmtManual(v *float64) string {
	if v == nil {
		return "—"
	}
	return strconv.FormatFloat(*v, 'f', -1, 64)
}

// renderManual renders the manual-entry block: the stored entries and a form
// that saves (PUT) or deletes them through /api/manual-financials.
func renderManual(w http.ResponseWriter, company string, entries []models.ManualFinancials, defaultYear int) {
	fmt.Fprint(w, `<div class="card manual-card">
  <p class="hint">Показатели за год в млрд ₽ — обычно из годового отчёта МСФО группы. Если указан хоть один показатель
  отчётности, он <b>заменяет загруженную отчётность за этот год целиком</b> (РСБУ, CSV): незаполненные поля станут пустыми,
  рыночная капитализация сохранится, P/E и ROE пересчитаются. Если указаны только дивиденды — они просто дополняют загруженные данные.
  Автоматическая загрузка ручные данные не затирает; удаление записи возвращает загруженные цифры.</p>`)

	if len(entries) > 0 {
		fmt.Fprintf(w, `<div class="table-wrap"><table class="table"><thead><tr><th>Год</th>`)
		for _, f := range manualFields {
			fmt.Fprintf(w, `<th>%s</th>`, html.EscapeString(f.label))
		}
		fmt.Fprintf(w, `<th></th></tr></thead><tbody>`)
		for _, m := range entries {
			fmt.Fprintf(w, `<tr><td>%d</td>`, m.Year)
			for _, f := range manualFields {
				fmt.Fprintf(w, `<td>%s</td>`, fmtManual(f.get(m)))
			}
			fmt.Fprintf(w, `<td class="actions"><button type="button" class="btn btn-sm" onclick="editManual(%d)">Изменить</button><button type="button" class="btn btn-sm btn-danger" onclick="deleteManual(%d)">Удалить</button></td></tr>`,
				m.Year, m.Year)
		}
		fmt.Fprintf(w, `</tbody></table></div>`)
	}

	fmt.Fprintf(w, `<div class="manual-form">
    <label>Год<input id="manual-year" inputmode="numeric" value="%d"></label>`, defaultYear)
	for _, f := range manualFields {
		fmt.Fprintf(w, `<label>%s, млрд ₽<input id="manual-%s" inputmode="decimal" placeholder="—"></label>`,
			html.EscapeString(f.label), f.key)
	}
	keys := make([]string, len(manualFields))
	for i, f := range manualFields {
		keys[i] = f.key
	}
	fmt.Fprintf(w, `</div>
  <div class="manual-actions">
    <button type="button" class="btn btn-primary" onclick="saveManual()">Сохранить год</button>
    <button type="button" class="btn btn-ghost" onclick="clearManual()">Очистить форму</button>
    <span class="status" id="manual-status"></span>
  </div>
</div>
<script>
(function () {
  const company = %s;
  const keys = %s;
  const entries = %s || [];
  const $ = id => document.getElementById(id);
  const status = msg => { $('manual-status').textContent = msg; };

  // "1 234,5" -> 1234.5; "" -> null; anything else -> NaN.
  function parse(raw) {
    const s = raw.replace(/[\s ]/g, '').replace(',', '.');
    if (s === '') return null;
    return /^-?\d+(\.\d+)?$/.test(s) ? Number(s) : NaN;
  }

  window.editManual = function (year) {
    const e = entries.find(x => x.year === year);
    if (!e) return;
    $('manual-year').value = year;
    keys.forEach(k => { $('manual-' + k).value = e[k] === null || e[k] === undefined ? '' : String(e[k]).replace('.', ','); });
    status('Редактирование ' + year + ' года');
    $('manual-year').scrollIntoView({ behavior: 'smooth', block: 'center' });
  };

  window.clearManual = function () {
    keys.forEach(k => { $('manual-' + k).value = ''; });
    status('');
  };

  window.saveManual = async function () {
    const year = Number($('manual-year').value.trim());
    if (!Number.isInteger(year) || year < 1990) { status('Укажите год (не раньше 1990)'); return; }
    const body = { company: company, year: year };
    for (const k of keys) {
      const v = parse($('manual-' + k).value);
      if (Number.isNaN(v)) { status('Не число в поле «' + $('manual-' + k).parentElement.firstChild.textContent.trim() + '»'); return; }
      body[k] = v;
    }
    status('Сохранение…');
    try {
      const resp = await fetch('/api/manual-financials', {
        method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body)
      });
      if (!resp.ok) {
        const err = await resp.json().catch(() => ({}));
        throw new Error(err.error || resp.status);
      }
      location.reload();
    } catch (e) { status('Ошибка: ' + e.message); }
  };

  window.deleteManual = async function (year) {
    if (!confirm('Удалить ручные данные за ' + year + ' год? Вернутся загруженные цифры.')) return;
    status('Удаление…');
    try {
      const resp = await fetch('/api/manual-financials?company=' + encodeURIComponent(company) + '&year=' + year, { method: 'DELETE' });
      if (!resp.ok) {
        const err = await resp.json().catch(() => ({}));
        throw new Error(err.error || resp.status);
      }
      location.reload();
    } catch (e) { status('Ошибка: ' + e.message); }
  };
})();
</script>`, jsonForScript(company), jsonForScript(keys), jsonForScript(entries))
}
