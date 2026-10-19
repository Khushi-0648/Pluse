package server

const authJS = `// Client-side authentication validation bundle (S12 external script)
function validatePasswords() {
  const p1 = document.getElementById('new_password').value;
  const p2 = document.getElementById('confirm_password').value;
  if (p1.length < 8) {
    alert('Password must be at least 8 characters long.');
    return false;
  }
  if (!/[a-zA-Z]/.test(p1) || !/[0-9]/.test(p1)) {
    alert('Password must contain both letters and numbers.');
    return false;
  }
  if (p1.toLowerCase().includes('admin') || p1.toLowerCase().includes('password') || p1 === '12345678') {
    alert('Password is too common or easily guessable.');
    return false;
  }
  if (p1 !== p2) {
    alert('Passwords do not match. Please verify and try again.');
    return false;
  }
  return true;
}

document.addEventListener('DOMContentLoaded', () => {
  const form = document.getElementById('setupPasswordForm');
  if (form) {
    form.addEventListener('submit', (e) => {
      if (!validatePasswords()) {
        e.preventDefault();
      }
    });
  }
});
`
