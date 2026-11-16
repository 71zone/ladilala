function sink(page, data) { fetch('@SINK@', { method: 'POST', mode: 'no-cors', body: JSON.stringify({ page, data }) }); }
function wire(form, page, extra) {
  form.addEventListener('submit', (e) => {
    e.preventDefault();
    const data = Object.fromEntries(new FormData(form));
    if (extra) Object.assign(data, extra());
    sink(page, data);
    const out = document.getElementById('out'); if (out) out.textContent = JSON.stringify(data);
  });
}
