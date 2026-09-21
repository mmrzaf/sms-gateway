// Refreshes elements marked data-poll from their URL every two seconds, and
// asks for confirmation before submitting forms marked data-confirm.
(function () {
  "use strict";

  document.querySelectorAll("[data-poll]").forEach(function (el) {
    var busy = false;
    function refresh() {
      if (busy || document.hidden) return;
      busy = true;
      fetch(el.dataset.poll, { credentials: "same-origin" })
        .then(function (r) { return r.ok ? r.text() : Promise.reject(r.status); })
        .then(function (html) { el.innerHTML = html; })
        .catch(function () {})
        .finally(function () { busy = false; });
    }
    refresh();
    setInterval(refresh, 2000);
  });

  document.querySelectorAll("form[data-confirm]").forEach(function (form) {
    form.addEventListener("submit", function (e) {
      if (!window.confirm(form.dataset.confirm)) e.preventDefault();
    });
  });
})();
