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

// Start a play day: show which season the picked date falls in (the
// latest-starting one, as the server picks; the list is newest first).
document.querySelectorAll('[data-season-hint]').forEach(function (box) {
  var input = document.getElementById(box.getAttribute('data-season-hint'));
  if (!input) return;
  var none = box.querySelector('[data-none]');
  function apply() {
    var v = input.value, found = false;
    box.querySelectorAll('[data-from]').forEach(function (el) {
      var on = !found && v !== '' && v >= el.getAttribute('data-from') && v <= el.getAttribute('data-to');
      el.hidden = !on;
      if (on) found = true;
    });
    if (none) none.hidden = found || v === '';
  }
  input.addEventListener('input', apply);
  input.addEventListener('change', apply);
  apply();
});

// Guide: the side rail lists each section's subsections and marks the one
// being read (the last section whose top has scrolled past the top of the view).
(function () {
  var rail = document.querySelector('.guiderail');
  if (!rail) return;
  var items = [];
  rail.querySelectorAll('ol > li > a[href^="#"]').forEach(function (a) {
    var sec = document.getElementById(a.getAttribute('href').slice(1));
    if (!sec) return;
    var subs = [];
    var heads = sec.querySelectorAll('h3');
    if (heads.length) {
      var ul = document.createElement('ul');
      heads.forEach(function (h, i) {
        if (!h.id) h.id = sec.id + '-' + (i + 1);
        h.style.scrollMarginTop = '12px';
        var link = document.createElement('a');
        link.href = '#' + h.id;
        link.textContent = h.textContent;
        var li = document.createElement('li');
        li.appendChild(link);
        ul.appendChild(li);
        subs.push({ el: h, a: link });
      });
      a.parentNode.appendChild(ul);
    }
    items.push({ el: sec, a: a, subs: subs });
  });
  function pick(list) {
    var cur = null;
    list.forEach(function (it) { if (it.el.getBoundingClientRect().top <= 80) cur = it; });
    return cur;
  }
  // At the very bottom the last sections can't reach the top; take the last.
  function atBottom() {
    return window.innerHeight + window.scrollY >= document.documentElement.scrollHeight - 4;
  }
  var queued = false;
  function update() {
    queued = false;
    var cur = (atBottom() && items[items.length - 1]) || pick(items) || items[0];
    items.forEach(function (it) {
      var on = it === cur;
      it.a.classList.toggle('on', on && !pick(it.subs));
      it.a.parentNode.classList.toggle('open', on);
      var sub = on ? (atBottom() && it.subs.length ? null : pick(it.subs)) : null;
      it.subs.forEach(function (s) { s.a.classList.toggle('on', s === sub); });
    });
  }
  window.addEventListener('scroll', function () {
    if (!queued) { queued = true; window.requestAnimationFrame(update); }
  }, { passive: true });
  update();
})();

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

// Lock a form once it is really submitting (after data-confirm above had its
// say), so a slow connection at the court doesn't invite a second tap: submit
// buttons are disabled and the one pressed says "Menyimpan…" (or
// "Mengunggah…" with a file attached). Forms that create records also carry a
// one-time token, so the server ignores a duplicate that gets through anyway.
(function () {
  var labels = document.body.dataset;
  function buttons(form) {
    var list = Array.prototype.slice.call(form.querySelectorAll('button:not([type=button])'));
    if (form.id) list = list.concat(Array.prototype.slice.call(document.querySelectorAll('button[form="' + form.id + '"]')));
    return list;
  }
  function hasFile(form) {
    return Array.prototype.some.call(form.querySelectorAll('input[type=file]'), function (i) { return i.files && i.files.length; });
  }
  function unlock(form) {
    clearTimeout(form._unlockTimer);
    delete form.dataset.submitting;
    buttons(form).forEach(function (b) {
      b.disabled = false;
      b.classList.remove('busy');
      if (b.dataset.label) { b.textContent = b.dataset.label; delete b.dataset.label; }
    });
  }
  document.addEventListener('submit', function (e) {
    var form = e.target;
    if (e.defaultPrevented || form.method.toLowerCase() !== 'post') return;
    if (form.dataset.submitting) { e.preventDefault(); return; }
    form.dataset.submitting = '1';
    var pressed = e.submitter;
    var label = hasFile(form) ? labels.uploading : labels.saving;
    // Disable after the browser has collected the form data, not before.
    setTimeout(function () {
      buttons(form).forEach(function (b) { b.disabled = true; });
      if (pressed && label) {
        pressed.dataset.label = pressed.textContent;
        pressed.textContent = label;
        pressed.classList.add('busy');
      }
    }, 0);
    // If nothing comes back (the connection dropped), allow another try.
    form._unlockTimer = setTimeout(function () { unlock(form); }, 20000);
  });
  // Back/forward restores pages from the bfcache with the buttons still locked.
  window.addEventListener('pageshow', function (e) {
    if (e.persisted) document.querySelectorAll('form[data-submitting]').forEach(unlock);
  });
})();
