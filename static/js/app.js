document.addEventListener('DOMContentLoaded', function () {
  initPasswordToggles();
  initConfirmPasswordMatch();
  initPasswordStrengthMeter();
});

// Any button with data-password-toggle="<input id>" swaps that input between
// type="password" and type="text", and toggles its own eye / eye-off icons
// (two <svg data-icon-shown/data-icon-hidden> children).
function initPasswordToggles() {
  document.querySelectorAll('[data-password-toggle]').forEach(function (btn) {
    var input = document.getElementById(btn.getAttribute('data-password-toggle'));
    if (!input) return;

    var shown = btn.querySelector('[data-icon-shown]');
    var hidden = btn.querySelector('[data-icon-hidden]');

    btn.addEventListener('click', function () {
      var reveal = input.type === 'password';
      input.type = reveal ? 'text' : 'password';
      btn.setAttribute('aria-label', reveal ? 'Hide password' : 'Show password');
      if (shown && hidden) {
        shown.classList.toggle('hidden', reveal);
        hidden.classList.toggle('hidden', !reveal);
      }
    });
  });
}

// Live "passwords don't match" validation between #password and
// #confirm_password, wherever both are present on the page.
function initConfirmPasswordMatch() {
  var pw = document.getElementById('password');
  var confirm = document.getElementById('confirm_password');
  if (!pw || !confirm) return;

  var check = function () {
    confirm.setCustomValidity(confirm.value && confirm.value !== pw.value ? 'Passwords do not match' : '');
  };
  pw.addEventListener('input', check);
  confirm.addEventListener('input', check);
}

// Fills in a 4-segment strength bar (#password-strength containing four
// [data-bar] elements and one [data-label]) as the user types into
// #password. Purely cosmetic guidance, not a hard requirement.
function initPasswordStrengthMeter() {
  var pw = document.getElementById('password');
  var meter = document.getElementById('password-strength');
  if (!pw || !meter) return;

  var bars = meter.querySelectorAll('[data-bar]');
  var label = meter.querySelector('[data-label]');
  var labels = ['Too short', 'Weak', 'Okay', 'Good', 'Strong'];
  var colors = ['bg-danger', 'bg-danger', 'bg-warn', 'bg-brand', 'bg-success'];

  pw.addEventListener('input', function () {
    var score = passwordScore(pw.value);
    bars.forEach(function (bar, i) {
      bar.className = 'h-1 flex-1 rounded-full transition-colors duration-200 ' +
        (pw.value && i < score ? colors[score] : 'bg-editor-border');
    });
    if (label) label.textContent = pw.value ? labels[score] : '';
  });
}

function passwordScore(v) {
  if (v.length < 8) return 0;
  var score = 1;
  if (v.length >= 12) score++;
  if (/[A-Z]/.test(v) && /[a-z]/.test(v)) score++;
  if (/[0-9]/.test(v)) score++;
  if (/[^A-Za-z0-9]/.test(v)) score++;
  return Math.min(score, 4);
}
