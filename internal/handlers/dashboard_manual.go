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
.manual-card { background: var(--bg-secondary); border-radius: 12px; padding: 20px; border: 1px solid var(--border); margin-top: 24px; }
.manual-card p.hint { color: var(--text-secondary); font-size: 13px; margin: 0 0 14px; line-height: 1.5; }
.manual-card .table-wrap { overflow-x: auto; margin-bottom: 16px; }
.manual-card table { border-collapse: collapse; width: 100%; font-size: 13px; white-space: nowrap; }
.manual-card th, .manual-card td { padding: 6px 8px; border-bottom: 1px solid var(--border); text-align: right; }
.manual-card th:first-child, .manual-card td:first-child { text-align: left; }
.manual-card th { color: var(--text-muted); font-weight: 500; font-size: 12px; }
.manual-card td.actions button { margin-left: 6px; }
.manual-form { display: grid; grid-template-columns: repeat(auto-fill, minmax(190px, 1fr)); gap: 10px 14px; }
.manual-form label { display: flex; flex-direction: column; gap: 4px; font-size: 12px; color: var(--text-muted); }
.manual-form input { padding: 7px 9px; background: var(--bg-card); color: var(--text-primary); border: 1px solid var(--border); border-radius: 6px; font: inherit; font-size: 14px; }
.manual-actions { display: flex; gap: 10px; align-items: center; margin-top: 12px; flex-wrap: wrap; }
.manual-card button { background: var(--bg-card); color: var(--text-primary); border: 1px solid var(--border); padding: 6px 14px; border-radius: 6px; cursor: pointer; font-size: 13px; }
.manual-card button.primary { background: var(--accent); color: white; border-color: var(--accent); }
.manual-card .status { font-size: 12px; color: var(--text-muted); }
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
	fmt.Fprintf(w, `<style>%s</style>
<div class="manual-card">
  <div class="section-title" style="margin-top:0">Ручные данные (годовой отчёт МСФО)</div>
  <p class="hint">Показатели за год в млрд ₽ — обычно из годового отчёта МСФО группы. Если указан хоть один показатель
  отчётности, он <b>заменяет загруженную отчётность за этот год целиком</b> (РСБУ, CSV): незаполненные поля станут пустыми,
  рыночная капитализация сохранится, P/E и ROE пересчитаются. Если указаны только дивиденды — они просто дополняют загруженные данные.
  Автоматическая загрузка ручные данные не затирает; удаление записи возвращает загруженные цифры.</p>`, manualCSS)

	if len(entries) > 0 {
		fmt.Fprintf(w, `<div class="table-wrap"><table><thead><tr><th>Год</th>`)
		for _, f := range manualFields {
			fmt.Fprintf(w, `<th>%s</th>`, html.EscapeString(f.label))
		}
		fmt.Fprintf(w, `<th></th></tr></thead><tbody>`)
		for _, m := range entries {
			fmt.Fprintf(w, `<tr><td>%d</td>`, m.Year)
			for _, f := range manualFields {
				fmt.Fprintf(w, `<td>%s</td>`, fmtManual(f.get(m)))
			}
			fmt.Fprintf(w, `<td class="actions"><button type="button" onclick="editManual(%d)">Изменить</button><button type="button" onclick="deleteManual(%d)">Удалить</button></td></tr>`,
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
    <button type="button" class="primary" onclick="saveManual()">Сохранить год</button>
    <button type="button" onclick="clearManual()">Очистить форму</button>
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
