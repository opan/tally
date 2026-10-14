// Poster: scale the fixed 1024×1536 design to the screen, export it as PNG.
(function () {
  var frame = document.getElementById('posterFrame');
  var poster = document.getElementById('poster');
  if (!frame || !poster) return;
  var W = 1024, H = 1536;

  function fit() {
    var s = Math.min(1, frame.clientWidth / W);
    poster.style.transform = 'scale(' + s + ')';
    frame.style.height = Math.ceil(H * s) + 'px';
  }
  window.addEventListener('resize', fit);
  fit();

  // Shrink el's font so its widest child (or itself) fits maxW. Layout values
  // ignore the preview scale, so this measures the real 1024px canvas.
  function shrinkToFit(el, parts, maxW) {
    if (!el) return;
    el.style.fontSize = '';
    var widest = 0;
    parts.forEach(function (p) { widest = Math.max(widest, p.scrollWidth); });
    if (widest > maxW) {
      el.style.fontSize = (parseFloat(getComputedStyle(el).fontSize) * maxW / widest) + 'px';
    }
  }

  // Size rows so any board (up to 20 players) fills the table area.
  var TABLE_ROWS_H = 770;
  function layout() {
    var brand = poster.querySelector('.pz-brand');
    var titles = poster.querySelector('.pz-titles');
    if (titles) {
      var maxW = (brand ? brand.offsetLeft - 30 : W - 80) - titles.offsetLeft;
      shrinkToFit(titles, [].slice.call(titles.querySelectorAll('.pz-t1, .pz-t2')), maxW);
    }
    var badge = poster.querySelector('.pz-badge');
    if (badge && brand) {
      badge.style.whiteSpace = 'nowrap';
      shrinkToFit(badge, [badge], brand.clientWidth);
    }
    var n = poster.querySelectorAll('.pz-row').length;
    if (n > 0) {
      var per = TABLE_ROWS_H / n;
      var rowH = Math.min(66, per * 0.86);
      poster.style.setProperty('--pz-row-h', rowH + 'px');
      poster.style.setProperty('--pz-row-gap', Math.min(10, per - rowH) + 'px');
      poster.style.setProperty('--pz-name', Math.min(38, rowH * 0.58) + 'px');
      poster.style.setProperty('--pz-rank', Math.min(40, rowH * 0.6) + 'px');
    }
  }
  document.fonts.ready.then(layout);

  var opts = { width: W, height: H, pixelRatio: 1, style: { transform: 'none' } };
  var isSafari = /^((?!chrome|android).)*safari/i.test(navigator.userAgent);

  // Safari sometimes drops fonts/images on the first render, so warm it up.
  function render(kind) {
    return document.fonts.ready.then(function () {
      layout();
      var fn = kind === 'blob' ? htmlToImage.toBlob : htmlToImage.toPng;
      return isSafari ? fn(poster, opts).then(function () { return fn(poster, opts); }) : fn(poster, opts);
    });
  }

  function busy(btn, on) {
    if (on) { btn.dataset.label = btn.textContent; btn.textContent = btn.dataset.busy || '…'; btn.disabled = true; }
    else { btn.textContent = btn.dataset.label; btn.disabled = false; }
  }

  var dl = document.getElementById('posterDownload');
  var filename = dl.getAttribute('data-filename') || 'poster.png';
  dl.addEventListener('click', function () {
    busy(dl, true);
    render('png').then(function (url) {
      var a = document.createElement('a');
      a.href = url;
      a.download = filename;
      document.body.appendChild(a);
      a.click();
      a.remove();
    }).catch(function () { alert(dl.getAttribute('data-failed')); })
      .then(function () { busy(dl, false); });
  });

  // Share straight to WhatsApp & co. where the browser can share files.
  var share = document.getElementById('posterShare');
  var probe = typeof File === 'function' && new File([''], filename, { type: 'image/png' });
  if (share && probe && navigator.canShare && navigator.canShare({ files: [probe] })) {
    share.hidden = false;
    share.dataset.busy = dl.dataset.busy;
    share.addEventListener('click', function () {
      busy(share, true);
      render('blob').then(function (blob) {
        return navigator.share({ files: [new File([blob], filename, { type: 'image/png' })] });
      }).catch(function () {}).then(function () { busy(share, false); });
    });
  }
})();
