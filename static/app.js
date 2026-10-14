// Progressive enhancement only; every page works without JS.

// Dashboard: suggest a link from the tally name until the user edits it.
(function () {
  var name = document.getElementById('tallyName');
  var slug = document.getElementById('tallySlug');
  if (!name || !slug) return;
  function slugify(s) {
    return s.toLowerCase().replace(/[\s_]+/g, '-').replace(/[^a-z0-9-]/g, '')
      .replace(/-+/g, '-').replace(/^-|-$/g, '').slice(0, 40);
  }
  var edited = slug.value.length > 0;
  slug.addEventListener('input', function () { edited = slug.value.length > 0; });
  name.addEventListener('input', function () { if (!edited) slug.value = slugify(name.value); });
})();

// Copy-to-clipboard for any [data-copy="#selector"] button.
document.addEventListener('click', function (e) {
  var btn = e.target.closest('[data-copy]');
  if (!btn) return;
  var el = document.querySelector(btn.getAttribute('data-copy'));
  if (!el) return;
  function done() {
    var old = btn.textContent;
    btn.textContent = btn.getAttribute('data-done') || old;
    setTimeout(function () { btn.textContent = old; }, 1500);
  }
  el.select();
  if (navigator.clipboard && navigator.clipboard.writeText) {
    navigator.clipboard.writeText(el.value).then(done, function () { document.execCommand('copy'); done(); });
  } else {
    document.execCommand('copy');
    done();
  }
});

// Ask before destructive submits: <form data-confirm="Delete X?">.
document.addEventListener('submit', function (e) {
  var msg = e.target.getAttribute('data-confirm');
  if (msg && !window.confirm(msg)) e.preventDefault();
});

// Season form: show only the target field that applies to the chosen format.
document.querySelectorAll('[data-format-toggle]').forEach(function (fs) {
  var form = fs.closest('form');
  function apply() {
    var checked = fs.querySelector('input[name=format]:checked');
    var fmt = checked ? checked.value : 'continuous';
    form.querySelectorAll('[data-for-format]').forEach(function (el) {
      el.hidden = el.getAttribute('data-for-format').split(' ').indexOf(fmt) < 0;
    });
  }
  fs.addEventListener('change', apply);
  apply();
});

// Attendance: select all / clear for checkboxes named by data-check-all / data-check-none.
document.addEventListener('click', function (e) {
  var btn = e.target.closest('[data-check-all],[data-check-none]');
  if (!btn) return;
  var on = btn.hasAttribute('data-check-all');
  var name = btn.getAttribute(on ? 'data-check-all' : 'data-check-none');
  btn.closest('form').querySelectorAll('input[type=checkbox][name="' + name + '"]').forEach(function (cb) { cb.checked = on; });
});

// Leaderboard pickers apply on change (the form also has a <noscript> button).
document.querySelectorAll('form[data-autosubmit]').forEach(function (f) {
  f.addEventListener('change', function () { f.submit(); });
});

// Create-tally form: the type dropdown shows its description and the fields
// that apply to it (data-for-preset="a b").
document.querySelectorAll('select[data-preset-toggle]').forEach(function (sel) {
  var form = sel.closest('form');
  var help = document.getElementById(sel.getAttribute('aria-describedby'));
  function apply() {
    var opt = sel.options[sel.selectedIndex];
    if (help && opt) help.textContent = opt.getAttribute('data-help') || '';
    form.querySelectorAll('[data-for-preset]').forEach(function (el) {
      el.hidden = el.getAttribute('data-for-preset').split(' ').indexOf(sel.value) < 0;
    });
  }
  sel.addEventListener('change', apply);
  apply();
});

// Cancel buttons inside a <details>: clear the form and collapse the section.
document.addEventListener('click', function (e) {
  var btn = e.target.closest('[data-close-details]');
  if (!btn) return;
  var form = btn.closest('form');
  if (form) form.reset();
  var details = btn.closest('details');
  if (details) {
    details.open = false;
    details.querySelector('summary').focus();
  }
});
