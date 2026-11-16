// Keep keyboard focus on the same action when storage refreshes its row.
export function renderRetryList(list, rows) {
  const active = document.activeElement;
  const action = list.contains(active) ? active?.dataset.retryAction : undefined;
  list.replaceChildren(...rows);
  list.hidden = rows.length === 0;
  if (action) {
    const target = [...list.querySelectorAll("button")].find((button) => button.dataset.retryAction === action);
    target?.focus();
  }
}
