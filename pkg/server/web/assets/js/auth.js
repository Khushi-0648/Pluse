'use strict';
// Sign-in pages: show/hide password and live password rules. The rules mirror
// security.ValidatePassword on the server, which is the one that decides.
(function () {
  const BANNED = ['admin', 'password', 'passw0rd', 'root', 'qwerty', 'letmein', 'welcome', 'changeme', 'whatsapp', 'pulse'];
  const SEQUENCES = ['12345678', '87654321', 'abcdefgh'];

  // Mirrors security.Guessable on the server.
  const ROWS = ['1234567890', 'qwertyuiop', 'asdfghjkl', 'zxcvbnm'];
  const COMMON = new Set(['test', 'hello', 'india', 'love', 'iloveyou', 'shop', 'store', 'user', 'guest', 'login', 'secret', 'summer', 'monkey', 'dragon', 'sunshine', 'owner', 'business', 'office', 'mobile', 'abc', 'abcd']);
  function patterned(a, b) {
    const x = a.codePointAt(0), y = b.codePointAt(0);
    if (x === y || y === x + 1 || y === x - 1) return true;
    return ROWS.some((row) => { const i = row.indexOf(a), j = row.indexOf(b); return i >= 0 && j >= 0 && Math.abs(i - j) === 1; });
  }
  function guessable(lower) {
    const rs = Array.from(lower);
    let allRuns = true, start = 0;
    for (let i = 1; i <= rs.length; i++) {
      if (i < rs.length && patterned(rs[i - 1], rs[i])) continue;
      if (i - start < 3) { allRuns = false; break; }
      start = i;
    }
    if (allRuns) return true;
    const letters = lower.replace(/[^a-z]/g, ''), d = Array.from(lower.replace(/[^0-9]/g, ''));
    if (!COMMON.has(letters)) return false;
    if (d.length <= 4) return true;
    return d.every((c, i) => i === 0 || patterned(d[i - 1], c));
  }

  function check(p) {
    const lower = p.toLowerCase();
    return {
      length: p.length >= 8 && p.length <= 128,
      mix: /[a-zA-Z]/.test(p) && /[0-9]/.test(p),
      words: !BANNED.some((w) => lower.includes(w)) && !SEQUENCES.some((s) => lower.includes(s)) && !guessable(lower),
      variety: new Set(lower).size >= 4,
    };
  }

  document.addEventListener('click', (e) => {
    const btn = e.target.closest('[data-toggle-password]');
    if (!btn) return;
    const input = document.getElementById(btn.getAttribute('data-toggle-password'));
    if (!input) return;
    const show = input.type === 'password';
    input.type = show ? 'text' : 'password';
    btn.textContent = show ? 'Hide' : 'Show';
    btn.setAttribute('aria-pressed', String(show));
  });

  document.addEventListener('DOMContentLoaded', () => {
    const form = document.getElementById('setup-form');
    if (!form) return;
    const p1 = document.getElementById('new_password');
    const p2 = document.getElementById('confirm_password');
    const box = document.getElementById('client-error');
    const text = document.getElementById('client-error-text');
    const update = () => {
      const r = check(p1.value.trim());
      document.querySelectorAll('#pw-rules li').forEach((li) => li.classList.toggle('ok', !!r[li.dataset.rule]));
    };
    p1.addEventListener('input', update);
    form.addEventListener('submit', (e) => {
      const r = check(p1.value.trim());
      let msg = '';
      if (!r.length) msg = 'Use at least 8 characters.';
      else if (!r.mix) msg = 'Use both letters and numbers.';
      else if (!r.words) msg = 'Too easy to guess. Avoid words like admin or hello with simple numbers, and patterns like abcd1234 or qwer1234.';
      else if (!r.variety) msg = 'Use at least 4 different characters.';
      else if (p1.value !== p2.value) msg = 'The two passwords do not match.';
      if (msg) {
        e.preventDefault();
        text.textContent = msg;
        box.hidden = false;
        p1.focus();
      }
    });
  });
})();
