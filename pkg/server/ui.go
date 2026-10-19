package server

const loginHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Login - Web Auto</title>
  <style>
    * { box-sizing: border-box; margin: 0; padding: 0; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif; }
    body {
      background: radial-gradient(circle at 50% -10%, #dcfce7 0%, #f8fafc 65%);
      min-height: 100vh;
      display: flex;
      align-items: center;
      justify-content: center;
      padding: 24px;
      color: #0f172a;
    }
    .login-card {
      background: #ffffff;
      border: 1px solid #e2e8f0;
      border-radius: 16px;
      padding: 38px 34px;
      width: 100%;
      max-width: 420px;
      box-shadow: 0 10px 25px -5px rgba(0, 0, 0, 0.05), 0 8px 10px -6px rgba(0, 0, 0, 0.02);
      text-align: center;
    }
    h1 { font-size: 24px; font-weight: 700; color: #0f172a; margin-bottom: 6px; }
    p.sub { font-size: 13.5px; color: #64748b; margin-bottom: 24px; }
    .input-group { margin-bottom: 20px; text-align: left; }
    label { font-size: 12px; font-weight: 600; color: #475569; display: block; margin-bottom: 6px; text-transform: uppercase; letter-spacing: 0.5px; }
    input[type="password"] {
      width: 100%;
      background: #ffffff;
      border: 1px solid #cbd5e1;
      border-radius: 8px;
      padding: 12px 14px;
      color: #0f172a;
      font-size: 14.5px;
      outline: none;
      transition: all 0.2s ease;
    }
    input[type="password"]:focus {
      border-color: #059669;
      box-shadow: 0 0 0 3px rgba(5, 150, 105, 0.15);
    }
    button {
      width: 100%;
      background: #059669;
      color: #ffffff;
      font-weight: 600;
      border: none;
      border-radius: 8px;
      padding: 12px;
      font-size: 14.5px;
      cursor: pointer;
      transition: all 0.2s ease;
      box-shadow: 0 2px 4px rgba(5, 150, 105, 0.2);
    }
    button:hover { background: #047857; box-shadow: 0 4px 6px rgba(5, 150, 105, 0.25); }
    .alert-error {
      background: #fef2f2;
      border: 1px solid #fecaca;
      color: #b91c1c;
      padding: 11px 14px;
      border-radius: 8px;
      font-size: 13px;
      margin-bottom: 18px;
      display: flex;
      align-items: center;
      gap: 8px;
      text-align: left;
    }
    .icon {
      width: 16px;
      height: 16px;
      display: inline-block;
      vertical-align: -2px;
      stroke: currentColor;
      stroke-width: 2;
      fill: none;
      stroke-linecap: round;
      stroke-linejoin: round;
      flex-shrink: 0;
    }
  </style>
</head>
<body>
  <div class="login-card">
    <div style="margin: 0 auto 16px; width: 56px; height: 56px; display: flex; align-items: center; justify-content: center; filter: drop-shadow(0 4px 10px rgba(5, 150, 105, 0.25));">
      <svg width="56" height="56" viewBox="0 0 48 48" fill="none" xmlns="http://www.w3.org/2000/svg">
        <rect width="48" height="48" rx="13" fill="url(#loginBrandGrad)"/>
        <defs>
          <linearGradient id="loginBrandGrad" x1="0" y1="0" x2="48" y2="48" gradientUnits="userSpaceOnUse">
            <stop stop-color="#10B981"/>
            <stop offset="0.6" stop-color="#059669"/>
            <stop offset="1" stop-color="#047857"/>
          </linearGradient>
          <linearGradient id="loginBoltGrad" x1="20" y1="12" x2="28" y2="34" gradientUnits="userSpaceOnUse">
            <stop stop-color="#FFFFFF"/>
            <stop offset="1" stop-color="#F1F5F9"/>
          </linearGradient>
        </defs>
        <path d="M24 10C16.82 10 11 15.82 11 23C11 25.43 11.67 27.71 12.83 29.66L11.5 35L17.02 33.68C19.11 34.78 21.48 35.42 24 35.42C31.18 35.42 37 29.6 37 22.42C37 15.58 31.18 10 24 10Z" fill="white" fill-opacity="0.25"/>
        <path d="M26.5 13L17 25H23.5L21.5 35L31 23H24.5L26.5 13Z" fill="url(#loginBoltGrad)"/>
      </svg>
    </div>
    <h1>Web <span style="color: #059669; font-weight: 800;">Auto</span></h1>
    <p class="sub">WhatsApp Automation Platform</p>

    {{if .MustChange}}
    <div style="background: #eff6ff; border: 1px solid #bfdbfe; color: #1e40af; padding: 12px 14px; border-radius: 8px; font-size: 13px; margin-bottom: 20px; text-align: left; line-height: 1.45;">
      <svg class="icon" style="color: #2563eb; margin-top: 2px;" viewBox="0 0 24 24"><circle cx="8" cy="8" r="6"></circle><path d="M18.09 10.37A6 6 0 1 1 10.34 2.61M7 11l10 10m-3-3 2 2m2-4 2 2"></path></svg> <div><strong>First-Time Setup:</strong> A temporary master password was generated in your console. Enter it below to set your permanent password.</div>
    </div>
    {{end}}

    {{if .Error}}
    <div class="alert-error"><svg class="icon" viewBox="0 0 24 24"><circle cx="12" cy="12" r="10"></circle><line x1="15" y1="9" x2="9" y2="15"></line><line x1="9" y1="9" x2="15" y2="15"></line></svg> <span>{{.Error}}</span></div>
    {{end}}

    <form method="POST" action="/login">
      <div class="input-group">
        <label for="pwd">Admin Master Password</label>
        <input type="password" id="pwd" name="password" placeholder="Enter master password" required autofocus>
      </div>
      <button type="submit">Unlock Dashboard</button>
    </form>
  </div>
</body>
</html>`

const setupPasswordHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Setup Master Password - Web Auto</title>
  <style>
    * { box-sizing: border-box; margin: 0; padding: 0; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif; }
    body {
      background: radial-gradient(circle at 50% -10%, #dcfce7 0%, #f8fafc 65%);
      min-height: 100vh;
      display: flex;
      align-items: center;
      justify-content: center;
      padding: 24px;
      color: #0f172a;
    }
    .setup-card {
      background: #ffffff;
      border: 1px solid #e2e8f0;
      border-radius: 16px;
      padding: 38px 34px;
      width: 100%;
      max-width: 440px;
      box-shadow: 0 10px 25px -5px rgba(0, 0, 0, 0.05), 0 8px 10px -6px rgba(0, 0, 0, 0.02);
      text-align: center;
    }
    h1 { font-size: 24px; font-weight: 700; color: #0f172a; margin-bottom: 6px; }
    p.sub { font-size: 13.5px; color: #64748b; margin-bottom: 22px; line-height: 1.45; }
    .input-group { margin-bottom: 18px; text-align: left; }
    label { font-size: 12px; font-weight: 600; color: #475569; display: block; margin-bottom: 6px; text-transform: uppercase; letter-spacing: 0.5px; }
    input[type="password"] {
      width: 100%;
      background: #ffffff;
      border: 1px solid #cbd5e1;
      border-radius: 8px;
      padding: 12px 14px;
      color: #0f172a;
      font-size: 14.5px;
      outline: none;
      transition: all 0.2s ease;
    }
    input[type="password"]:focus {
      border-color: #059669;
      box-shadow: 0 0 0 3px rgba(5, 150, 105, 0.15);
    }
    button {
      width: 100%;
      background: #059669;
      color: #ffffff;
      font-weight: 600;
      border: none;
      border-radius: 8px;
      padding: 12px;
      font-size: 14.5px;
      cursor: pointer;
      transition: all 0.2s ease;
      margin-top: 8px;
      box-shadow: 0 2px 4px rgba(5, 150, 105, 0.2);
    }
    button:hover { background: #047857; box-shadow: 0 4px 6px rgba(5, 150, 105, 0.25); }
    .alert-error {
      background: #fef2f2;
      border: 1px solid #fecaca;
      color: #b91c1c;
      padding: 11px 14px;
      border-radius: 8px;
      font-size: 13px;
      margin-bottom: 18px;
      display: flex;
      align-items: center;
      gap: 8px;
      text-align: left;
    }
    .notice {
      background: #ecfdf5;
      border: 1px solid #a7f3d0;
      color: #065f46;
      padding: 12px 14px;
      border-radius: 8px;
      font-size: 12.5px;
      margin-bottom: 20px;
      display: flex;
      align-items: center;
      gap: 8px;
      text-align: left;
      line-height: 1.45;
    }
    .icon {
      width: 16px;
      height: 16px;
      display: inline-block;
      vertical-align: -2px;
      stroke: currentColor;
      stroke-width: 2;
      fill: none;
      stroke-linecap: round;
      stroke-linejoin: round;
      flex-shrink: 0;
    }
  </style>
</head>
<body>
  <div class="setup-card">
    <div style="margin: 0 auto 16px; width: 56px; height: 56px; display: flex; align-items: center; justify-content: center; filter: drop-shadow(0 4px 10px rgba(5, 150, 105, 0.25));">
      <svg width="56" height="56" viewBox="0 0 48 48" fill="none" xmlns="http://www.w3.org/2000/svg">
        <rect width="48" height="48" rx="13" fill="#059669"/>
        <path d="M24 14C20.69 14 18 16.69 18 20V23H16C14.9 23 14 23.9 14 25V33C14 34.1 14.9 35 16 35H32C33.1 35 34 34.1 34 33V25C34 23.9 33.1 23 32 23H30V20C30 16.69 27.31 14 24 14ZM24 16C26.21 16 28 17.79 28 20V23H20V20C20 17.79 21.79 16 24 16Z" fill="#FFFFFF"/>
      </svg>
    </div>
    <h1>Set Master Password</h1>
    <p class="sub">Configure a permanent master password to protect your WhatsApp automation instance.</p>

    <div class="notice">
      <svg class="icon" style="color: #2563eb;" viewBox="0 0 24 24"><rect x="3" y="11" width="18" height="11" rx="2" ry="2"></rect><path d="M7 11V7a5 5 0 0 1 10 0v4"></path></svg> <span><strong>Security Policy:</strong> Minimum 8 characters with letters and numbers.</span>
    </div>

    {{if .Error}}
    <div class="alert-error"><svg class="icon" viewBox="0 0 24 24"><circle cx="12" cy="12" r="10"></circle><line x1="15" y1="9" x2="9" y2="15"></line><line x1="9" y1="9" x2="15" y2="15"></line></svg> <span>{{.Error}}</span></div>
    {{end}}

    <form method="POST" action="/setup-password" id="setupPasswordForm">
      <div class="input-group">
        <label for="new_password">New Master Password</label>
        <input type="password" id="new_password" name="new_password" placeholder="At least 8 characters (letters & numbers)" minlength="8" required autofocus>
      </div>
      <div class="input-group">
        <label for="confirm_password">Confirm Master Password</label>
        <input type="password" id="confirm_password" name="confirm_password" placeholder="Re-type new password" minlength="8" required>
      </div>
      <button type="submit">Save &amp; Continue to Dashboard</button>
    </form>
  </div>
  <script src="/static/auth.js" defer></script>
</body>
</html>`

const dashboardHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Web Auto</title>
  <link rel="manifest" href="/manifest.json">
  <meta name="theme-color" content="#10b981">
  <link rel="icon" type="image/svg+xml" href="/app-icon.svg">
  <style>
    :root {
      /* Brand Color Tokens (X5 Design System) */
      --primary: #059669;
      --primary-hover: #047857;
      --primary-subtle: #ecfdf5;
      --primary-border: #a7f3d0;
      
      --bg-body: #f8fafc;
      --bg-card: #ffffff;
      --bg-card-subtle: #f8fafc;
      --bg-card-hover: #f1f5f9;
      --border: #e2e8f0;
      --border-accent: rgba(5, 150, 105, 0.35);
      
      --text-primary: #0f172a;
      --text-secondary: #475569;
      --text-muted: #64748b;
      
      --danger: #ef4444;
      --danger-hover: #dc2626;
      --danger-subtle: #fef2f2;
      --danger-border: #fecaca;
      
      --warning: #f59e0b;
      --warning-hover: #d97706;
      --warning-subtle: #fffbeb;
      --warning-border: #fde68a;
      
      --info: #0284c7;
      --info-hover: #0369a1;
      --info-subtle: #f0f9ff;
      --info-border: #bae6fd;
      
      /* Typography Tokens */
      --font-sans: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif;
      --font-mono: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace;
      
      --text-xs: 11px;
      --text-sm: 13px;
      --text-base: 14px;
      --text-md: 15px;
      --text-lg: 18px;
      --text-xl: 20px;
      --text-2xl: 24px;
      
      --weight-regular: 400;
      --weight-medium: 500;
      --weight-semibold: 600;
      --weight-bold: 700;
      
      /* Spacing Scale */
      --space-1: 4px;
      --space-2: 8px;
      --space-3: 12px;
      --space-4: 16px;
      --space-5: 20px;
      --space-6: 24px;
      --space-8: 32px;
      
      /* Border Radiuses */
      --radius-sm: 6px;
      --radius-md: 8px;
      --radius-lg: 12px;
      --radius-xl: 16px;
      --radius-full: 9999px;
      
      /* Elevation & Shadows */
      --shadow-xs: 0 1px 2px rgba(0, 0, 0, 0.04);
      --shadow-sm: 0 1px 3px rgba(0, 0, 0, 0.06), 0 1px 2px rgba(0, 0, 0, 0.03);
      --shadow-md: 0 4px 6px -1px rgba(0, 0, 0, 0.07), 0 2px 4px -2px rgba(0, 0, 0, 0.04);
      --shadow-lg: 0 10px 15px -3px rgba(0, 0, 0, 0.08), 0 4px 6px -4px rgba(0, 0, 0, 0.03);
      --shadow-xl: 0 20px 25px -5px rgba(0, 0, 0, 0.08), 0 8px 10px -6px rgba(0, 0, 0, 0.03);
      
      /* Focus Ring & Transitions */
      --focus-ring: 0 0 0 3px rgba(5, 150, 105, 0.2);
      --transition-fast: 150ms cubic-bezier(0.4, 0, 0.2, 1);
      --transition-normal: 200ms cubic-bezier(0.4, 0, 0.2, 1);
      
      --sidebar-width: 260px;
    }

    :focus-visible {
      outline: 2px solid var(--primary);
      outline-offset: 2px;
    }

    * { box-sizing: border-box; margin: 0; padding: 0; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif; }
    body { background-color: var(--bg-body); color: var(--text-primary); display: flex; min-height: 100vh; overflow-x: hidden; }

    /* App Container */
    #sidebar {
      width: var(--sidebar-width);
      background: #ffffff;
      border-right: 1px solid var(--border);
      display: flex;
      flex-direction: column;
      position: fixed;
      top: 0;
      bottom: 0;
      left: 0;
      z-index: 100;
      box-shadow: 2px 0 8px rgba(0, 0, 0, 0.02);
      transition: transform 0.25s ease;
    }
    #main-content {
      margin-left: var(--sidebar-width);
      flex: 1;
      display: flex;
      flex-direction: column;
      min-height: 100vh;
      width: calc(100% - var(--sidebar-width));
      background-color: var(--bg-body);
    }

    /* Brand Header */
    .brand-box {
      padding: 20px 18px;
      display: flex;
      align-items: center;
      gap: 12px;
      border-bottom: 1px solid var(--border);
    }
    .brand-icon {
      width: 40px;
      height: 40px;
      border-radius: 10px;
      background: linear-gradient(135deg, #10b981 0%, #059669 100%);
      display: flex;
      align-items: center;
      justify-content: center;
      box-shadow: 0 4px 12px rgba(16, 185, 129, 0.25);
      flex-shrink: 0;
    }
    .brand-title { font-size: 16px; font-weight: 700; color: #0f172a; line-height: 1.2; }
    .brand-sub { font-size: 11px; color: var(--text-muted); }

    /* Nav Links */
    .nav-list { list-style: none; padding: 12px 10px; flex: 1; overflow-y: auto; }
    .nav-item { margin-bottom: 4px; }
    .nav-link {
      display: flex;
      align-items: center;
      gap: 12px;
      padding: 10px 14px;
      color: var(--text-secondary);
      text-decoration: none;
      font-size: 13px;
      font-weight: 500;
      border-radius: 8px;
      transition: all 0.15s ease;
      cursor: pointer;
    }
    .nav-link:hover { background: #f1f5f9; color: #0f172a; }
    .nav-link.active {
      background: #ecfdf5;
      color: #059669;
      font-weight: 600;
      border: 1px solid rgba(16, 185, 129, 0.3);
    }
    .nav-badge {
      margin-left: auto;
      background: #fef3c7;
      border: 1px solid #fde68a;
      color: #d97706;
      font-size: 10px;
      font-weight: 700;
      padding: 2px 6px;
      border-radius: 10px;
    }
    .icon {
      width: 16px;
      height: 16px;
      display: inline-block;
      vertical-align: -2px;
      stroke: currentColor;
      stroke-width: 2;
      fill: none;
      stroke-linecap: round;
      stroke-linejoin: round;
      flex-shrink: 0;
    }
    .icon-sm {
      width: 14px;
      height: 14px;
      display: inline-block;
      vertical-align: -1px;
      stroke: currentColor;
      stroke-width: 2;
      fill: none;
      stroke-linecap: round;
      stroke-linejoin: round;
      flex-shrink: 0;
    }
    .icon-lg {
      width: 20px;
      height: 20px;
      display: inline-block;
      vertical-align: -3px;
      stroke: currentColor;
      stroke-width: 2;
      fill: none;
      stroke-linecap: round;
      stroke-linejoin: round;
      flex-shrink: 0;
    }
    .icon-xl {
      width: 32px;
      height: 32px;
      display: inline-block;
      stroke: #94a3b8;
      stroke-width: 1.75;
      fill: none;
      stroke-linecap: round;
      stroke-linejoin: round;
      flex-shrink: 0;
    }
    @keyframes spin { 100% { transform: rotate(360deg); } }
    .spinner { animation: spin 1s linear infinite; }
    .nav-icon {
      width: 17px;
      height: 17px;
      flex-shrink: 0;
      stroke: currentColor;
      stroke-width: 2;
      stroke-linecap: round;
      stroke-linejoin: round;
      fill: none;
      opacity: 0.85;
      transition: all 0.15s ease;
    }
    .nav-link.active .nav-icon {
      opacity: 1;
      stroke: #059669;
    }

    /* Top Bar */
    #topbar {
      height: 64px;
      background: rgba(255, 255, 255, 0.92);
      backdrop-filter: blur(12px);
      border-bottom: 1px solid var(--border);
      display: flex;
      align-items: center;
      justify-content: space-between;
      padding: 0 24px;
      position: sticky;
      top: 0;
      z-index: 90;
    }
    .topbar-left { display: flex; align-items: center; gap: 14px; }
    .mobile-menu-btn { display: none; background: transparent; border: none; color: #0f172a; font-size: 20px; cursor: pointer; }
    .page-title { font-size: 18px; font-weight: 700; color: #0f172a; }
    .topbar-right { display: flex; align-items: center; gap: 12px; }

    /* Security Notice Bar */
    .sec-notice-bar {
      background: #fffbeb;
      border-bottom: 1px solid #fde68a;
      padding: 10px 24px;
      display: flex;
      align-items: center;
      justify-content: space-between;
      font-size: 13px;
      color: #92400e;
    }

    /* Status Pill */
    .status-pill {
      display: inline-flex;
      align-items: center;
      gap: 7px;
      background: #ffffff;
      border: 1px solid var(--border);
      padding: 6px 12px;
      border-radius: 20px;
      font-size: 12px;
      font-weight: 600;
      color: #0f172a;
      box-shadow: 0 1px 2px rgba(0, 0, 0, 0.05);
    }
    .status-dot { width: 8px; height: 8px; border-radius: 50%; background: #94a3b8; }
    .status-dot.online { background: #10b981; box-shadow: 0 0 8px rgba(16, 185, 129, 0.6); }
    .status-dot.connecting { background: #f59e0b; box-shadow: 0 0 8px rgba(245, 158, 11, 0.6); }

    /* Tabs Content Container */
    .tab-content { display: none; padding: 24px; flex: 1; }
    .tab-content.active { display: block; }

    /* Layout Cards */
    .card {
      background: #ffffff;
      border: 1px solid var(--border);
      border-radius: 14px;
      padding: 22px;
      margin-bottom: 22px;
      box-shadow: 0 1px 3px 0 rgba(0, 0, 0, 0.04), 0 1px 2px -1px rgba(0, 0, 0, 0.03);
    }
    .card-title {
      font-size: 15px;
      font-weight: 700;
      color: #0f172a;
      margin-bottom: 14px;
      display: flex;
      align-items: center;
      justify-content: space-between;
    }
    .card-desc { font-size: 13px; color: var(--text-secondary); margin-bottom: 16px; line-height: 1.5; }

    /* Grid Layouts */
    .grid-2 { display: grid; grid-template-columns: 1fr 1fr; gap: 20px; }
    .grid-3 { display: grid; grid-template-columns: repeat(3, 1fr); gap: 20px; }
    .grid-4 { display: grid; grid-template-columns: repeat(4, 1fr); gap: 16px; }

    /* Metric Cards */
    .metric-card {
      background: #ffffff;
      border: 1px solid var(--border);
      border-radius: 14px;
      padding: 18px;
      display: flex;
      align-items: center;
      gap: 16px;
      box-shadow: 0 1px 3px rgba(0,0,0,0.04);
    }
    .metric-icon {
      width: 48px;
      height: 48px;
      border-radius: 12px;
      display: flex;
      align-items: center;
      justify-content: center;
      font-size: 22px;
      flex-shrink: 0;
    }
    .metric-val { font-size: 22px; font-weight: 800; color: #0f172a; line-height: 1.2; }
    .metric-lbl { font-size: 11px; color: var(--text-muted); text-transform: uppercase; letter-spacing: 0.5px; margin-top: 2px; font-weight: 600; }

    /* Form Controls */
    .form-group { margin-bottom: 16px; }
    label { display: block; font-size: 12px; font-weight: 600; color: #334155; margin-bottom: 6px; }
    input[type="text"], input[type="password"], input[type="datetime-local"], input[type="email"], select, textarea {
      width: 100%;
      background: #ffffff;
      border: 1px solid #cbd5e1;
      border-radius: 8px;
      padding: 10px 12px;
      color: #0f172a;
      font-size: 13px;
      outline: none;
      transition: all 0.15s ease;
    }
    input:focus, select:focus, textarea:focus {
      border-color: #10b981;
      box-shadow: 0 0 0 3px rgba(16, 185, 129, 0.15);
    }
    input::placeholder, textarea::placeholder {
      color: #94a3b8;
    }
    textarea { resize: vertical; min-height: 80px; }

    /* Buttons */
    .btn {
      display: inline-flex;
      align-items: center;
      justify-content: center;
      gap: 8px;
      padding: 9px 16px;
      border-radius: 8px;
      font-size: 13px;
      font-weight: 600;
      border: 1px solid transparent;
      cursor: pointer;
      transition: all 0.15s ease;
      text-decoration: none;
    }
    .btn-primary { background: #10b981; color: #ffffff; border-color: #059669; }
    .btn-primary:hover { background: #059669; color: #ffffff; }
    .btn-secondary { background: #f8fafc; color: #334155; border: 1px solid #cbd5e1; }
    .btn-secondary:hover { background: #f1f5f9; color: #0f172a; border-color: #94a3b8; }
    .btn-danger { background: #fef2f2; color: #dc2626; border: 1px solid #fecaca; }
    .btn-danger:hover { background: #dc2626; color: #ffffff; }
    .btn-sm { padding: 5px 11px; font-size: 11px; border-radius: 6px; }

    /* Tables */
    .table-container {
      width: 100%;
      overflow-x: auto;
      border-radius: 10px;
      border: 1px solid var(--border);
      background: #ffffff;
    }
    table { width: 100%; border-collapse: collapse; text-align: left; font-size: 13px; }
    th {
      background: #f8fafc;
      color: #475569;
      font-weight: 600;
      text-transform: uppercase;
      font-size: 11px;
      letter-spacing: 0.5px;
      padding: 12px 14px;
      border-bottom: 1px solid var(--border);
    }
    td {
      padding: 12px 14px;
      border-bottom: 1px solid #f1f5f9;
      color: #334155;
      vertical-align: middle;
    }
    tr:last-child td { border-bottom: none; }
    tr:hover td { background: #f8fafc; }

    /* Badges */
    .badge {
      display: inline-flex;
      align-items: center;
      gap: 4px;
      padding: 3px 8px;
      border-radius: 6px;
      font-size: 11px;
      font-weight: 600;
    }
    .badge-in { background: #e0f2fe; color: #0284c7; border: 1px solid #bae6fd; }
    .badge-out { background: #dcfce7; color: #16a34a; border: 1px solid #bbf7d0; }
    .badge-auto { background: #f3e8ff; color: #9333ea; border: 1px solid #e9d5ff; }
    .badge-warn { background: #fef3c7; color: #d97706; border: 1px solid #fde68a; }
    .badge-danger { background: #fee2e2; color: #dc2626; border: 1px solid #fecaca; }

    /* Two-Way Team Inbox */
    .inbox-container {
      display: flex;
      height: calc(100vh - 160px);
      min-height: 580px;
      background: #ffffff;
      border: 1px solid var(--border);
      border-radius: 14px;
      overflow: hidden;
      box-shadow: 0 2px 8px rgba(0,0,0,0.04);
    }
    .inbox-list-pane {
      width: 320px;
      border-right: 1px solid var(--border);
      display: flex;
      flex-direction: column;
      background: #ffffff;
    }
    .inbox-search-box { padding: 12px; border-bottom: 1px solid var(--border); background: #f8fafc; }
    .inbox-contacts { flex: 1; overflow-y: auto; list-style: none; }
    .inbox-item {
      padding: 12px 14px;
      border-bottom: 1px solid #f1f5f9;
      cursor: pointer;
      display: flex;
      gap: 12px;
      align-items: center;
      transition: background 0.15s ease;
    }
    .inbox-item:hover { background: #f8fafc; }
    .inbox-item.active { background: #ecfdf5; border-left: 3px solid #10b981; }
    .inbox-avatar {
      width: 38px;
      height: 38px;
      border-radius: 50%;
      background: #e2e8f0;
      display: flex;
      align-items: center;
      justify-content: center;
      font-weight: 700;
      color: #334155;
      font-size: 14px;
      flex-shrink: 0;
      border: 1px solid #cbd5e1;
    }
    .inbox-meta { flex: 1; min-width: 0; }
    .inbox-name { font-size: 13px; font-weight: 600; color: #0f172a; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
    .inbox-last-msg { font-size: 11px; color: var(--text-muted); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; margin-top: 2px; }

    .inbox-chat-pane { flex: 1; display: flex; flex-direction: column; background: #efeae2; }
    .inbox-chat-header {
      padding: 14px 20px;
      border-bottom: 1px solid #e2e8f0;
      background: #f0f2f5;
      display: flex;
      align-items: center;
      justify-content: space-between;
    }
    .inbox-chat-body {
      flex: 1;
      padding: 20px;
      overflow-y: auto;
      display: flex;
      flex-direction: column;
      gap: 10px;
      background: #efeae2;
    }
    .bubble {
      max-width: 68%;
      padding: 9px 13px;
      border-radius: 8px;
      font-size: 13px;
      line-height: 1.45;
      word-break: break-word;
      position: relative;
      box-shadow: 0 1px 0.5px rgba(11, 20, 26, 0.13);
    }
    .bubble-incoming {
      align-self: flex-start;
      background: #ffffff;
      color: #111b21;
      border-top-left-radius: 0;
    }
    .bubble-outgoing {
      align-self: flex-end;
      background: #d9fdd3;
      color: #111b21;
      border-top-right-radius: 0;
    }
    .bubble-time {
      font-size: 10px;
      color: #667781;
      margin-top: 3px;
      text-align: right;
    }

    .quick-chips {
      padding: 8px 16px;
      background: #f0f2f5;
      border-top: 1px solid #e2e8f0;
      display: flex;
      gap: 8px;
      overflow-x: auto;
      white-space: nowrap;
    }
    .chip {
      background: #ffffff;
      border: 1px solid #cbd5e1;
      border-radius: 16px;
      padding: 4px 11px;
      font-size: 11px;
      color: #334155;
      cursor: pointer;
      box-shadow: 0 1px 2px rgba(0,0,0,0.03);
      transition: all 0.15s ease;
    }
    .chip:hover { background: #ecfdf5; color: #059669; border-color: rgba(16, 185, 129, 0.4); }

    .inbox-composer {
      padding: 12px 16px;
      background: #f0f2f5;
      border-top: 1px solid #e2e8f0;
      display: flex;
      align-items: center;
      gap: 10px;
    }

    /* Modal System - NO BACKDROP LEFT GUARANTEE */
    .modal-backdrop {
      position: fixed;
      top: 0; left: 0; right: 0; bottom: 0;
      background: rgba(15, 23, 42, 0.45);
      backdrop-filter: blur(4px);
      display: flex;
      align-items: center;
      justify-content: center;
      z-index: 1000;
      padding: 20px;
      opacity: 0;
      visibility: hidden;
      pointer-events: none;
      transition: opacity 0.2s ease, visibility 0.2s ease;
    }
    .modal-backdrop.open {
      opacity: 1;
      visibility: visible;
      pointer-events: auto;
    }
    .modal-card {
      background: #ffffff;
      border: 1px solid #e2e8f0;
      border-radius: 14px;
      width: 100%;
      max-width: 520px;
      padding: 24px;
      box-shadow: 0 20px 25px -5px rgba(0, 0, 0, 0.1), 0 8px 10px -6px rgba(0, 0, 0, 0.1);
      position: relative;
      transform: scale(0.97);
      transition: transform 0.2s ease;
    }
    .modal-backdrop.open .modal-card {
      transform: scale(1);
    }
    .modal-header { display: flex; align-items: center; justify-content: space-between; margin-bottom: 16px; }
    .modal-title { font-size: 16px; font-weight: 700; color: #0f172a; }
    .modal-close { background: transparent; border: none; color: #64748b; font-size: 22px; cursor: pointer; line-height: 1; padding: 2px 6px; border-radius: 6px; }
    .modal-close:hover { color: #0f172a; background: #f1f5f9; }

    /* Toast Notifications */
    #toast-container {
      position: fixed;
      bottom: 24px;
      right: 24px;
      z-index: 2000;
      display: flex;
      flex-direction: column;
      gap: 10px;
    }
    .toast {
      min-width: 280px;
      max-width: 400px;
      background: #ffffff;
      border: 1px solid #e2e8f0;
      border-radius: 10px;
      padding: 12px 16px;
      color: #0f172a;
      font-size: 13px;
      font-weight: 500;
      box-shadow: 0 10px 25px -5px rgba(0, 0, 0, 0.1), 0 8px 10px -6px rgba(0, 0, 0, 0.05);
      display: flex;
      align-items: center;
      gap: 10px;
      animation: slideIn 0.25s ease-out;
    }
    .toast.success { border-left: 4px solid #10b981; }
    .toast.error { border-left: 4px solid #ef4444; }
    .toast.info { border-left: 4px solid #0284c7; }
    .toast.warn { border-left: 4px solid #f59e0b; }
    @keyframes slideIn { from { transform: translateX(100%); opacity: 0; } to { transform: translateX(0); opacity: 1; } }

    /* Empty States */
    .empty-state {
      padding: 48px 24px;
      text-align: center;
      color: var(--text-muted);
    }
    .empty-icon { font-size: 38px; margin-bottom: 12px; opacity: 0.8; }
    .empty-text { font-size: 14px; font-weight: 600; color: #0f172a; margin-bottom: 6px; }
    .empty-sub { font-size: 12px; color: var(--text-muted); max-width: 360px; margin: 0 auto 16px; }

    /* Responsive Media Queries (X1) */
    @media (max-width: 900px) {
      #sidebar { transform: translateX(-100%); width: 240px; }
      #sidebar.open { transform: translateX(0); }
      #main-content { margin-left: 0; width: 100%; }
      .mobile-menu-btn { display: block; }
      .grid-4 { grid-template-columns: repeat(2, 1fr); }
      .grid-3 { grid-template-columns: 1fr; }
      .grid-2 { grid-template-columns: 1fr; }
      .inbox-list-pane { width: 240px; }
    }
    @media (max-width: 600px) {
      #topbar { height: 54px; padding: 0 12px; }
      .topbar-left { gap: 8px; }
      .page-title { font-size: 15px; }
      .topbar-right { gap: 6px; }
      .topbar-right .btn-secondary { padding: 4px 8px; font-size: 11px; }
      .status-pill { padding: 4px 8px; font-size: 11px; gap: 5px; }
      .grid-4 { grid-template-columns: 1fr; }
      .inbox-container { flex-direction: column; height: auto; min-height: 500px; }
      .inbox-list-pane { width: 100%; height: 200px; border-right: none; border-bottom: 1px solid var(--border); }
    }
    @media (max-width: 420px) {
      .topbar-right .btn-anti-ban span.btn-label { display: none; }
      .topbar-right .btn-anti-ban { padding: 4px 6px; }
      .page-title { max-width: 110px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 14px; }
      .status-pill span#statusText { font-size: 10px; }
    }
  </style>
</head>
<body>

  <!-- Toast Container -->
  <div id="toast-container"></div>

  <!-- Sidebar -->
  <nav id="sidebar">
    <div class="brand-box">
      <div class="brand-icon">
        <svg width="24" height="24" viewBox="0 0 48 48" fill="none">
          <path d="M24 10C16.82 10 11 15.82 11 23C11 25.43 11.67 27.71 12.83 29.66L11.5 35L17.02 33.68C19.11 34.78 21.48 35.42 24 35.42C31.18 35.42 37 29.6 37 22.42C37 15.58 31.18 10 24 10Z" fill="white" fill-opacity="0.3"/>
          <path d="M26.5 13L17 25H23.5L21.5 35L31 23H24.5L26.5 13Z" fill="white"/>
        </svg>
      </div>
      <div>
        <div class="brand-title">Web <span style="color: #10b981;">Auto</span></div>
        <div class="brand-sub">WhatsApp Automation</div>
      </div>
    </div>

    <ul class="nav-list">
      <li class="nav-item">
        <a class="nav-link active" data-tab="dashboard">
          <svg class="nav-icon" viewBox="0 0 24 24"><rect x="3" y="3" width="7" height="9"></rect><rect x="14" y="3" width="7" height="5"></rect><rect x="14" y="12" width="7" height="9"></rect><rect x="3" y="16" width="7" height="5"></rect></svg>
          <span>Dashboard</span>
        </a>
      </li>
      <li class="nav-item">
        <a class="nav-link" data-tab="messages">
          <svg class="nav-icon" viewBox="0 0 24 24"><path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"></path></svg>
          <span>Team Inbox</span>
          <span id="navAttentionBadge" class="nav-badge" style="display: none;">0</span>
        </a>
      </li>
      <li class="nav-item">
        <a class="nav-link" data-tab="scheduled">
          <svg class="nav-icon" viewBox="0 0 24 24"><circle cx="12" cy="12" r="10"></circle><polyline points="12 6 12 12 16 14"></polyline></svg>
          <span>Scheduled ({{.ScheduledCount}})</span>
        </a>
      </li>
      <li class="nav-item">
        <a class="nav-link" data-tab="contacts">
          <svg class="nav-icon" viewBox="0 0 24 24"><path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"></path><circle cx="9" cy="7" r="4"></circle><path d="M23 21v-2a4 4 0 0 0-3-3.87"></path><path d="M16 3.13a4 4 0 0 1 0 7.75"></path></svg>
          <span>Contacts (<span id="navContactsCount">{{.ContactsCount}}</span>)</span>
        </a>
      </li>
      <li class="nav-item">
        <a class="nav-link" data-tab="broadcast">
          <svg class="nav-icon" viewBox="0 0 24 24"><polygon points="11 5 6 9 2 9 2 15 6 15 11 19 11 5"></polygon><path d="M19.07 4.93a10 10 0 0 1 0 14.14M15.54 8.46a5 5 0 0 1 0 7.07"></path></svg>
          <span>Broadcasts</span>
        </a>
      </li>
      <li class="nav-item">
        <a class="nav-link" data-tab="templates">
          <svg class="nav-icon" viewBox="0 0 24 24"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"></path><polyline points="14 2 14 8 20 8"></polyline><line x1="16" y1="13" x2="8" y2="13"></line><line x1="16" y1="17" x2="8" y2="17"></line></svg>
          <span>Templates ({{.TemplatesCount}})</span>
        </a>
      </li>
      <li class="nav-item">
        <a class="nav-link" data-tab="rules">
          <svg class="nav-icon" viewBox="0 0 24 24"><polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2"></polygon></svg>
          <span>Auto-Reply Rules ({{.RulesCount}})</span>
        </a>
      </li>
      <li class="nav-item">
        <a class="nav-link" data-tab="webhooks">
          <svg class="nav-icon" viewBox="0 0 24 24"><path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"></path><path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"></path></svg>
          <span>Webhooks &amp; API</span>
        </a>
      </li>
      <li class="nav-item">
        <a class="nav-link" data-tab="settings">
          <svg class="nav-icon" viewBox="0 0 24 24"><circle cx="12" cy="12" r="3"></circle><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-2 2 2 2 0 0 1-2-2v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1-2-2 2 2 0 0 1 2-2h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 2-2 2 2 0 0 1 2 2v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 2 2 2 2 0 0 1-2 2h-.09a1.65 1.65 0 0 0-1.51 1z"></path></svg>
          <span>Settings &amp; Security</span>
        </a>
      </li>
    </ul>

    <div style="padding: 14px 18px; border-top: 1px solid var(--border); font-size: 11px; color: var(--text-muted);">
      <div>Web Auto v1.3.0</div>
    </div>
  </nav>

  <!-- Main Content Area -->
  <main id="main-content">

    <!-- Topbar -->
    <header id="topbar">
      <div class="topbar-left">
        <button class="mobile-menu-btn" data-action="toggleSidebar" aria-label="Toggle Navigation"><svg class="icon-lg" viewBox="0 0 24 24"><line x1="3" y1="12" x2="21" y2="12"></line><line x1="3" y1="6" x2="21" y2="6"></line><line x1="3" y1="18" x2="21" y2="18"></line></svg></button>
        <span class="page-title" id="pageTitle">Dashboard</span>
      </div>

      <div class="topbar-right">
        <button class="btn btn-secondary btn-sm btn-anti-ban" data-action="openComplianceModal" title="Anti-Ban Guidelines"><svg class="icon" viewBox="0 0 24 24"><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"></path></svg> <span class="btn-label">Anti-Ban Guide</span></button>
        <div class="status-pill">
          <div id="statusDot" class="status-dot {{if eq .Status "Connected"}}online{{else}}connecting{{end}}"></div>
          <span id="statusText">{{.Status}}</span>
        </div>
        <a href="javascript:void(0)" data-action="userLogout" class="btn btn-secondary btn-sm" style="color: #f87171;">Logout</a>
      </div>
    </header>

    {{if .IsDefaultPassword}}
    <div class="sec-notice-bar">
      <span style="display: inline-flex; align-items: center; gap: 6px;"><svg class="icon" style="color: #f59e0b;" viewBox="0 0 24 24"><path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"></path><line x1="12" y1="9" x2="12" y2="13"></line><line x1="12" y1="17" x2="12.01" y2="17"></line></svg> <strong>Security Notice:</strong> You are using default password. Please update in Settings.</span>
      <button class="btn btn-sm btn-primary" data-action="openPasswordModal">Change Password</button>
    </div>
    {{end}}

    <!-- TAB: DASHBOARD -->
    <div id="tab-dashboard" class="tab-content active">
      <!-- WhatsApp Device Pairing Hero (Shown when NOT logged in) -->
      <div id="pairingHero" class="card" style="background: #ffffff; border: 1px solid var(--border); box-shadow: 0 4px 16px rgba(0,0,0,0.04); display: {{if .WhatsApp.LoggedIn}}none{{else}}block{{end}};">
        <div class="card-title">
          <span style="display: inline-flex; align-items: center; gap: 8px;"><svg class="icon" viewBox="0 0 24 24"><rect x="5" y="2" width="14" height="20" rx="2" ry="2"></rect><line x1="12" y1="18" x2="12.01" y2="18"></line></svg> WhatsApp Connection</span>
          <button class="btn btn-secondary btn-sm" data-action="relinkDevice"><svg class="icon-sm" viewBox="0 0 24 24"><polyline points="23 4 23 10 17 10"></polyline><polyline points="1 20 1 14 7 14"></polyline><path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15"></path></svg> Reset</button>
        </div>

        <div class="grid-2">
          <!-- QR Canvas -->
          <div style="background: #f8fafc; border: 1px solid var(--border); border-radius: 10px; padding: 18px; text-align: center; display: flex; flex-direction: column; align-items: center; justify-content: center;">
            <div style="font-size: 13px; font-weight: 600; margin-bottom: 12px; color: var(--text-primary);">Method 1: Scan QR Code</div>
            <div style="display: inline-block; background: #fff; padding: 10px; border-radius: 8px; box-shadow: 0 2px 8px rgba(0,0,0,0.06); border: 1px solid #e2e8f0; min-width: 200px; min-height: 200px;">
              <img id="qrImage" src="/api/v1/qr?t={{.Uptime}}" alt="WhatsApp QR Code" style="width: 180px; height: 180px; display: block; object-fit: contain; margin: 0 auto;">
            </div>
            <div id="qrStatusMsg" style="font-size: 12px; color: var(--text-secondary); margin-top: 10px; min-height: 18px;">
              Scan with WhatsApp &gt; Linked Devices
            </div>
            <button type="button" id="btnRegenQR" class="btn btn-primary btn-sm" data-action="regenerateQRCode" style="margin-top: 10px; width: 200px; display: flex; align-items: center; justify-content: center; gap: 6px; font-weight: 600;">
              <svg class="icon-sm" viewBox="0 0 24 24"><polyline points="23 4 23 10 17 10"></polyline><polyline points="1 20 1 14 7 14"></polyline><path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15"></path></svg> <span>Regenerate QR</span>
            </button>
          </div>

          <!-- Pairing Code -->
          <div style="background: #f8fafc; border: 1px solid var(--border); border-radius: 10px; padding: 18px; display: flex; flex-direction: column; justify-content: space-between;">
            <div>
              <div style="font-size: 13px; font-weight: 600; margin-bottom: 8px; color: var(--text-primary);">Method 2: Link with Phone Number</div>
              <div style="font-size: 12px; color: var(--text-secondary); margin-bottom: 12px; line-height: 1.4;">Enter your WhatsApp phone number with country code (e.g. <code>+919876543210</code> or <code>+14155552671</code>):</div>
              <div style="display: flex; gap: 8px;">
                <input type="text" id="pairPhoneInput" placeholder="+919876543210" style="flex: 1;">
                <button class="btn btn-primary" id="btnGetPairCode" data-action="requestPairCode">Get Code</button>
              </div>
            </div>
            <div id="pairCodeBox" style="display: none; margin-top: 14px; padding: 14px; background: rgba(16, 185, 129, 0.08); border: 1px dashed var(--primary); border-radius: 8px; text-align: center;">
              <div style="font-size: 12px; color: var(--text-secondary);">Enter this 8-digit code on your WhatsApp mobile app:</div>
              <div id="pairCodeVal" style="font-size: 28px; font-weight: 800; font-family: monospace; color: #059669; letter-spacing: 4px; margin: 8px 0;"></div>
              <div id="pairCodeStatus" style="font-size: 11px; color: #0284c7;">WhatsApp &gt; Linked Devices &gt; Link with phone number</div>
            </div>
          </div>
        </div>
      </div>

      <!-- Logged In Details Hero (Shown when logged in) -->
      <div id="loggedInHero" class="card" style="background: linear-gradient(135deg, #ffffff 0%, #f0fdf4 100%); border: 1px solid #bbf7d0; box-shadow: 0 4px 16px rgba(16, 185, 129, 0.08); display: {{if .WhatsApp.LoggedIn}}block{{else}}none{{end}};">
        <div class="card-title" style="display: flex; justify-content: space-between; align-items: center; border-bottom: 1px solid #e2e8f0; padding-bottom: 14px; margin-bottom: 16px;">
          <div style="display: flex; align-items: center; gap: 10px;">
            <div style="width: 12px; height: 12px; background: #10b981; border-radius: 50%; box-shadow: 0 0 10px rgba(16, 185, 129, 0.6);"></div>
            <span style="font-size: 17px; font-weight: 700; color: #0f172a;">WhatsApp Account Connected</span>
          </div>
          <button class="btn btn-secondary btn-sm" data-action="relinkDevice" style="background: #fef2f2; border-color: #fecaca; color: #dc2626;"><svg class="icon-sm" viewBox="0 0 24 24"><path d="M18.36 6.64a9 9 0 1 1-12.73 0"></path><line x1="12" y1="2" x2="12" y2="12"></line></svg> Disconnect</button>
        </div>

        <div class="grid-3" style="gap: 16px;">
          <div style="background: #ffffff; border: 1px solid #e2e8f0; border-radius: 10px; padding: 14px; box-shadow: 0 1px 3px rgba(0,0,0,0.03);">
            <div style="font-size: 11px; text-transform: uppercase; letter-spacing: 0.5px; color: #64748b; font-weight: 600;">Account / Push Name</div>
            <div id="loginName" style="font-size: 18px; font-weight: 700; color: #0284c7; margin-top: 4px;">{{if .WhatsApp.PushName}}{{.WhatsApp.PushName}}{{else}}WhatsApp User{{end}}</div>
          </div>
          <div style="background: #ffffff; border: 1px solid #e2e8f0; border-radius: 10px; padding: 14px; box-shadow: 0 1px 3px rgba(0,0,0,0.03);">
            <div style="font-size: 11px; text-transform: uppercase; letter-spacing: 0.5px; color: #64748b; font-weight: 600;">Phone Number</div>
            <div id="loginPhone" style="font-size: 18px; font-weight: 700; color: #059669; margin-top: 4px;">{{if .WhatsApp.Phone}}+{{.WhatsApp.Phone}}{{else}}Active{{end}}</div>
          </div>
          <div style="background: #ffffff; border: 1px solid #e2e8f0; border-radius: 10px; padding: 14px; box-shadow: 0 1px 3px rgba(0,0,0,0.03);">
            <div style="font-size: 11px; text-transform: uppercase; letter-spacing: 0.5px; color: #64748b; font-weight: 600;">Status &amp; Automation</div>
            <div id="loginStatus" style="font-size: 15px; font-weight: 600; color: #10b981; margin-top: 6px; display: flex; align-items: center; gap: 6px;">
              <svg class="icon" viewBox="0 0 24 24"><path d="M22 11.08V12a10 10 0 1 1-5.93-9.14"></path><polyline points="22 4 12 14.01 9 11.01"></polyline></svg> <span>Active &amp; Ready</span>
            </div>
          </div>
        </div>
      </div>

      <!-- Quick Message Dispatcher -->
      <div class="card">
        <div class="card-title"><svg class="icon" viewBox="0 0 24 24"><polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2"></polygon></svg> Direct Dispatch</div>
        <form id="quickSendForm">
          <div class="grid-2">
            <div class="form-group">
              <label for="qsPhone">Phone Number</label>
              <input type="text" id="qsPhone" placeholder="+CountryCode Number (e.g. +1234567890)" required>
            </div>
            <div class="form-group">
              <label for="qsName">Recipient Name (Optional)</label>
              <input type="text" id="qsName" placeholder="Recipient Name (optional)">
            </div>
          </div>
          <div class="form-group">
            <label for="qsMsg">Message Content</label>
            <textarea id="qsMsg" placeholder="Hello &#123;&#123;name&#125;&#125;! Welcome to our service." rows="3" required></textarea>
          </div>
          <div class="grid-2" style="align-items: center;">
            <div class="form-group" style="margin-bottom: 0;">
              <label for="qsFile">Attach Media (Optional, 16MB max)</label>
              <input type="file" id="qsFile" accept="image/*,.pdf,.doc,.docx,.txt,.zip">
            </div>
            <div style="text-align: right; margin-top: 18px;">
              <button type="submit" class="btn btn-primary" id="qsBtn">Send Message</button>
            </div>
          </div>
        </form>
      </div>
    </div>

    <!-- TAB: TWO-WAY TEAM INBOX (C5) -->
    <div id="tab-messages" class="tab-content">
      <div class="inbox-container">
        <!-- Left: Conversations List -->
        <div class="inbox-list-pane">
          <div style="padding: 10px 14px; border-bottom: 1px solid var(--border); display: flex; justify-content: space-between; align-items: center; background: #f8fafc;">
            <div style="font-size: 13px; font-weight: 700; color: #0f172a; display: flex; align-items: center; gap: 6px;">
              <svg class="icon" viewBox="0 0 24 24"><path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"></path></svg> <span>Inbox</span>
            </div>
            <div style="display: flex; gap: 6px;">
              <button type="button" class="btn btn-secondary btn-sm" data-action="refreshInbox" title="Refresh conversations" style="padding: 4px 8px; font-size: 11px;">
                <svg class="icon-sm" viewBox="0 0 24 24"><polyline points="23 4 23 10 17 10"></polyline><polyline points="1 20 1 14 7 14"></polyline><path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15"></path></svg> <span>Refresh</span>
              </button>
              <button type="button" class="btn btn-secondary btn-sm" data-action="clearAllInbox" title="Clear entire inbox" style="padding: 4px 8px; font-size: 11px; color: #dc2626; border-color: #fecaca; background: #fef2f2;">
                <svg class="icon-sm" viewBox="0 0 24 24"><polyline points="3 6 5 6 21 6"></polyline><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"></path></svg> <span>Clear</span>
              </button>
            </div>
          </div>
          <div class="inbox-search-box">
            <input type="text" id="inboxSearchInput" placeholder="Search chats or phone..." oninput="filterInboxContacts()">
          </div>
          <ul class="inbox-contacts" id="inboxContactsList">
            <!-- Dynamically populated -->
          </ul>
        </div>

        <!-- Right: Chat Room -->
        <div class="inbox-chat-pane">
          <div class="inbox-chat-header" style="display: flex; justify-content: space-between; align-items: center; padding: 12px 18px;">
            <div>
              <div id="chatContactName" style="font-size: 14px; font-weight: 700; color: #0f172a;">Select a chat to begin</div>
              <div id="chatContactPhone" style="font-size: 11px; color: var(--text-muted); font-family: monospace;">-</div>
            </div>
            <div style="display: flex; align-items: center; gap: 8px;">
              <div id="chatHandoverControls" style="display: none; align-items: center; gap: 8px;">
                <span class="badge badge-warn"><svg class="icon-sm" viewBox="0 0 24 24"><path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"></path><line x1="12" y1="9" x2="12" y2="13"></line><line x1="12" y1="17" x2="12.01" y2="17"></line></svg> Human Needed</span>
                <button class="btn btn-secondary btn-sm" data-action="resumeBotForCurrentChat"><svg class="icon-sm" viewBox="0 0 24 24"><polygon points="5 3 19 12 5 21 5 3"></polygon></svg> Resume Bot</button>
              </div>
              <div id="chatActionControls" style="display: none; align-items: center; gap: 6px;">
                <button type="button" class="btn btn-secondary btn-sm" data-action="refreshCurrentChat" title="Refresh messages in this chat" style="padding: 5px 10px; font-size: 12px;">
                  <svg class="icon-sm" viewBox="0 0 24 24"><polyline points="23 4 23 10 17 10"></polyline><polyline points="1 20 1 14 7 14"></polyline><path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15"></path></svg> <span>Refresh</span>
                </button>
                <button type="button" class="btn btn-secondary btn-sm" data-action="clearCurrentChat" title="Clear this chat history" style="padding: 5px 10px; font-size: 12px; color: #dc2626; border-color: #fecaca; background: #fef2f2;">
                  <svg class="icon-sm" viewBox="0 0 24 24"><polyline points="3 6 5 6 21 6"></polyline><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"></path></svg> <span>Clear</span>
                </button>
              </div>
            </div>
          </div>

          <div class="inbox-chat-body" id="inboxChatStream">
            <div class="empty-state">
              <div class="empty-icon"><svg class="icon-xl" viewBox="0 0 24 24"><path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"></path></svg></div>
              <div class="empty-text">No conversation selected</div>
              <div class="empty-sub">Pick a conversation from the left pane or select a contact.</div>
            </div>
          </div>

          <!-- Quick Reply Chips -->
          <div class="quick-chips">
            <span class="chip" data-action="insertQuickReply" data-reply="Hello! How can I help you today?">Greeting</span>
            <span class="chip" data-action="insertQuickReply" data-reply="One moment please, looking into your request.">Wait</span>
            <span class="chip" data-action="insertQuickReply" data-reply="Here is our pricing and service overview.">Services</span>
            <span class="chip" data-action="insertQuickReply" data-reply="May we schedule a quick call with our specialist?">Call</span>
            <span class="chip" data-action="insertQuickReply" data-reply="Your request has been resolved. Thank you!">Resolved</span>
          </div>

          <!-- Attachment Preview Badge -->
          <div id="inboxFileBadge" style="display: none; padding: 6px 12px; background: #f0f9ff; border: 1px solid #bae6fd; border-radius: 6px; margin: 0 14px 8px 14px; font-size: 12px; color: #0284c7; align-items: center; justify-content: space-between;">
            <span id="inboxFileName" style="display: inline-flex; align-items: center; gap: 6px;"><svg class="icon-sm" viewBox="0 0 24 24"><path d="M21.44 11.05l-9.19 9.19a6 6 0 0 1-8.49-8.49l9.19-9.19a4 4 0 0 1 5.66 5.66l-9.2 9.19a2 2 0 0 1-2.83-2.83l8.49-8.48"></path></svg> <span>file.jpg</span></span>
            <button type="button" data-action="clearInboxFile" style="background: none; border: none; color: #dc2626; cursor: pointer; font-size: 14px; font-weight: bold;">&times;</button>
          </div>

          <!-- Composer -->
          <div class="inbox-composer">
            <input type="file" id="inboxFile" style="display: none;" onchange="handleInboxFileSelect(this)">
            <button type="button" class="btn btn-secondary btn-sm" data-action="chooseInboxFile" title="Attach file"><svg class="icon" viewBox="0 0 24 24"><path d="M21.44 11.05l-9.19 9.19a6 6 0 0 1-8.49-8.49l9.19-9.19a4 4 0 0 1 5.66 5.66l-9.2 9.19a2 2 0 0 1-2.83-2.83l8.49-8.48"></path></svg></button>
            <input type="text" id="inboxInput" placeholder="Type message... (Press Enter to send)" onkeydown="if(event.key==='Enter') sendInboxMessage()" style="flex: 1;">
            <button type="button" class="btn btn-primary" data-action="sendInboxMessage">Send</button>
          </div>
        </div>
      </div>
    </div>

    <!-- TAB: SCHEDULED MESSAGES -->
    <div id="tab-scheduled" class="tab-content">
      <div class="card">
        <div class="card-title">
          <span style="display: inline-flex; align-items: center; gap: 8px;"><svg class="icon" viewBox="0 0 24 24"><circle cx="12" cy="12" r="10"></circle><polyline points="12 6 12 12 16 14"></polyline></svg> Scheduled Messages</span>
          <button class="btn btn-primary btn-sm" data-action="openScheduleModal">+ New Schedule</button>
        </div>

        <div style="display: flex; gap: 12px; margin-bottom: 16px;">
          <input type="text" id="schFilter" placeholder="Search scheduled messages..." oninput="filterScheduledLive()" style="max-width: 360px;">
        </div>

        <div class="table-container">
          <table>
            <thead>
              <tr>
                <th>Recipient</th>
                <th>Scheduled For</th>
                <th>Message Preview</th>
                <th>Status</th>
                <th>Action</th>
              </tr>
            </thead>
            <tbody id="scheduledTableBody">
              {{range .Scheduled}}
              <tr>
                <td>
                  <strong style="color: #0284c7; font-family: monospace;">{{.Recipient}}</strong>
                  {{if .RecipientName}}<div style="font-size: 12px; color: #0f172a; font-weight: 500;">{{.RecipientName}}</div>{{else if .Name}}<div style="font-size: 12px; color: #0f172a; font-weight: 500;">{{.Name}}</div>{{end}}
                </td>
                <td style="font-size: 12px; color: var(--text-secondary);">{{.ScheduledAt.Format "02 Jan 2006 15:04"}}</td>
                <td style="max-width: 320px; word-break: break-word;">{{.Message}}</td>
                <td>
                  <span class="badge {{if eq .Status "pending"}}badge-warn{{else if eq .Status "completed"}}badge-out{{else}}badge-danger{{end}}">
                    {{.Status}}
                  </span>
                </td>
                <td>
                  <div style="display: flex; gap: 6px;">
                    {{if eq .Status "failed"}}
                    <button class="btn btn-secondary btn-sm" data-id="{{.ID}}" data-action="retryScheduled" title="Retry scheduled job">Retry</button>
                    {{end}}
                    <button class="btn btn-danger btn-sm" data-id="{{.ID}}" data-action="deleteScheduled">Cancel</button>
                  </div>
                </td>
              </tr>
              {{else}}
              <tr><td colspan="5" style="text-align: center; color: var(--text-muted); padding: 32px;">No scheduled messages active. Click '+ New Schedule' to create one.</td></tr>
              {{end}}
            </tbody>
          </table>
        </div>
      </div>
    </div>

    <!-- TAB: CONTACTS CRM -->
    <div id="tab-contacts" class="tab-content">
      <div class="card">
        <div class="card-title">
          <span style="display: inline-flex; align-items: center; gap: 8px;"><svg class="icon" viewBox="0 0 24 24"><path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"></path><circle cx="9" cy="7" r="4"></circle><path d="M23 21v-2a4 4 0 0 0-3-3.87"></path><path d="M16 3.13a4 4 0 0 1 0 7.75"></path></svg> Contacts</span>
          <div style="display: flex; gap: 8px;">
            <button class="btn btn-secondary btn-sm" data-action="exportContactsCSV"><svg class="icon-sm" viewBox="0 0 24 24"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"></path><polyline points="7 10 12 15 17 10"></polyline><line x1="12" y1="15" x2="12" y2="3"></line></svg> Export CSV</button>
            <button class="btn btn-primary btn-sm" data-action="openContactModal">+ Add Contact</button>
          </div>
        </div>

        <div style="display: flex; gap: 12px; margin-bottom: 16px;">
          <input type="text" id="contactFilter" placeholder="Search contacts..." oninput="filterContactsLive()" style="max-width: 360px;">
        </div>

        <div class="table-container">
          <table>
            <thead>
              <tr>
                <th>Phone</th>
                <th>Name</th>
                <th>Email</th>
                <th>Tags</th>
                <th>Status</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody id="contactsTableBody">
              <!-- Dynamically rendered safely via loadContacts() using selectChat(this.getAttribute('data-phone'), this.getAttribute('data-name')) -->
            </tbody>
          </table>
        </div>
      </div>

      <!-- CSV Bulk Import Card (C13) -->
      <div class="card">
        <div class="card-title"><svg class="icon" viewBox="0 0 24 24"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"></path></svg> Bulk Import CSV</div>
        <form id="csvImportForm">
          <div class="form-group">
            <textarea id="csvData" placeholder="phone,name,email,tags,notes&#10;+12345678901,Customer Name,customer@example.com,VIP;Lead,Key account note" rows="4"></textarea>
          </div>
          <button type="submit" class="btn btn-secondary">Import Contacts</button>
          <div id="csvImportFeedback" style="display:none; margin-top:12px; font-size:12px; border-radius:6px; padding:10px; border:1px solid #e2e8f0; background:#f8fafc;"></div>
        </form>
      </div>
    </div>

    <!-- TAB: BROADCAST CAMPAIGNS (C11) -->
    <div id="tab-broadcast" class="tab-content">
      <div class="card">
        <div class="card-title"><svg class="icon" viewBox="0 0 24 24"><path d="M3 11l18-5v12L3 13v-2z"></path><path d="M11.6 16.8a3 3 0 1 1-5.8-1.6"></path></svg> Broadcast Campaigns</div>

        <form id="broadcastForm">
          <div class="grid-2">
            <div class="form-group">
              <label for="bcAudience">Target Audience Segment</label>
              <select id="bcAudience">
                <option value="all">All Eligible Contacts (Excluding DND)</option>
                {{range .Tags}}
                <option value="tag:{{.}}">Contacts with Tag: {{.}}</option>
                {{end}}
              </select>
            </div>
            <div class="form-group">
              <label for="bcTemplate">Template (Optional)</label>
              <select id="bcTemplate" onchange="applyTemplateToBroadcast(this.value)">
                <option value="">-- Custom Text Message --</option>
                {{range .Templates}}
                <option value="{{.ID}}">{{.Name}} ({{.Category}})</option>
                {{end}}
              </select>
            </div>
          </div>

          <div class="form-group">
            <label for="bcMessage">Broadcast Message Content</label>
            <textarea id="bcMessage" rows="4" placeholder="Hello &#123;&#123;name&#125;&#125;! We are delighted to share our newest update..." required></textarea>
          </div>

          <div class="grid-3" style="background: #f8fafc; padding: 14px; border-radius: 8px; border: 1px solid var(--border); margin-bottom: 16px;">
            <div>
              <label>Pacing Delay (Seconds)</label>
              <div style="font-size: 12px; color: #059669; font-weight: 700;">4 to 10s (Randomized Jitter)</div>
            </div>
            <div>
              <label>Circuit Breaker</label>
              <div style="font-size: 12px; color: #d97706; font-weight: 700;">Stop on 5 consecutive fails</div>
            </div>
            <div>
              <label>Opt-Out Compliance</label>
              <div style="font-size: 12px; color: #0284c7; font-weight: 700;">Automatic STOP exclusion</div>
            </div>
          </div>

          <button type="submit" class="btn btn-primary" id="startBroadcastBtn">Start Campaign Dispatch</button>
        </form>

        <div id="broadcastProgressBox" style="display: none; margin-top: 20px; padding: 16px; background: rgba(16, 185, 129, 0.08); border: 1px solid var(--border-accent); border-radius: 8px;">
          <div style="display: flex; justify-content: space-between; align-items: center; font-size: 13px; font-weight: 600; margin-bottom: 8px;">
            <span>Campaign Delivery Progress</span>
            <div style="display: flex; align-items: center; gap: 10px;">
              <span id="bcProgressStats">0 / 0</span>
              <button type="button" class="btn btn-secondary btn-sm" id="bcCancelBtn" data-action="cancelBroadcast" style="color: #dc2626; border-color: #fecaca; background: #fef2f2; padding: 3px 8px; font-size: 11px;"><svg class="icon-sm" viewBox="0 0 24 24"><circle cx="12" cy="12" r="10"></circle><rect x="9" y="9" width="6" height="6"></rect></svg> Cancel</button>
            </div>
          </div>
          <div style="height: 8px; background: #e2e8f0; border-radius: 4px; overflow: hidden;">
            <div id="bcProgressBar" style="width: 0%; height: 100%; background: #10b981; transition: width 0.3s;"></div>
          </div>
        </div>
      </div>
    </div>

    <!-- TAB: TEMPLATES -->
    <div id="tab-templates" class="tab-content">
      <div class="card">
        <div class="card-title">
          <span style="display: inline-flex; align-items: center; gap: 8px;"><svg class="icon" viewBox="0 0 24 24"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"></path><polyline points="14 2 14 8 20 8"></polyline><line x1="16" y1="13" x2="8" y2="13"></line><line x1="16" y1="17" x2="8" y2="17"></line><polyline points="10 9 9 9 8 9"></polyline></svg> Templates</span>
          <button class="btn btn-primary btn-sm" data-action="openTemplateModal">+ New Template</button>
        </div>

        <div style="display: flex; gap: 12px; margin-bottom: 16px;">
          <input type="text" id="tmplFilter" placeholder="Search templates..." oninput="filterTemplatesLive()" style="max-width: 360px;">
        </div>

        <div class="table-container">
          <table>
            <thead>
              <tr>
                <th>Name</th>
                <th>Category</th>
                <th>Content Preview</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody id="templatesTableBody">
              {{range .Templates}}
              <tr>
                <td><strong style="color: #0f172a;">{{.Name}}</strong></td>
                <td><span class="badge badge-in">{{.Category}}</span></td>
                <td style="max-width: 360px; word-break: break-word; color: #334155;">{{.Content}}</td>
                <td>
                  <button class="btn btn-danger btn-sm" data-id="{{.ID}}" data-action="deleteTemplate">Delete</button>
                </td>
              </tr>
              {{else}}
              <tr><td colspan="4" style="text-align: center; color: var(--text-muted); padding: 32px;">No templates created. Click '+ New Template' to add one.</td></tr>
              {{end}}
            </tbody>
          </table>
        </div>
      </div>
    </div>

    <!-- TAB: AUTOMATION RULES (D8) -->
    <div id="tab-rules" class="tab-content">
      <div class="card">
        <div class="card-title">
          <span style="display: inline-flex; align-items: center; gap: 8px;"><svg class="icon" viewBox="0 0 24 24"><polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2"></polygon></svg> Auto-Reply Rules</span>
          <button class="btn btn-primary btn-sm" data-action="openRuleModal">+ New Rule</button>
        </div>

        <!-- Sandbox Rule Tester -->
        <div style="background: #f8fafc; border: 1px solid var(--border); border-radius: 10px; padding: 16px; margin-bottom: 20px;">
          <label style="font-weight: 700; color: #0f172a; margin-bottom: 8px; display: inline-flex; align-items: center; gap: 6px;"><svg class="icon-sm" viewBox="0 0 24 24"><path d="M10 2v7.31L4.69 18.5A2 2 0 0 0 6.42 22h11.16a2 2 0 0 0 1.73-3.5L14 9.31V2"></path><line x1="8.5" y1="2" x2="15.5" y2="2"></line><line x1="7" y1="14" x2="17" y2="14"></line></svg> Rule Simulator</label>
          <div style="display: flex; gap: 10px;">
            <input type="text" id="sandboxInput" placeholder="Test message (e.g. 'hi', 'pricing', 'support')..." style="flex: 1;">
            <button class="btn btn-secondary" data-action="simulateRule">Simulate Reply</button>
          </div>
          <div id="sandboxResult" style="display: none; margin-top: 10px; font-size: 13px; padding: 12px; border-radius: 8px; background: #f0f9ff; border: 1px solid #bae6fd;">
            <div id="sandboxReplyText" style="white-space: pre-wrap; color: #0f172a; font-weight: 500;"></div>
          </div>
        </div>

        <div style="display: flex; gap: 12px; margin-bottom: 16px;">
          <input type="text" id="ruleFilter" placeholder="Search rules..." oninput="filterRulesLive()" style="max-width: 360px;">
        </div>

        <div class="table-container">
          <table>
            <thead>
              <tr>
                <th>Rule Name</th>
                <th>Type</th>
                <th>Pattern Keywords</th>
                <th>Reply Content</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody id="rulesTableBody">
              {{range .Rules}}
              <tr>
                <td><strong style="color: #0f172a;">{{.Name}}</strong></td>
                <td><span class="badge badge-auto">{{.Type}}</span></td>
                <td><code style="font-size: 12px; color: #0284c7; font-weight: 600;">{{.Pattern}}</code></td>
                <td style="max-width: 320px; word-break: break-word; white-space: pre-wrap; color: #334155;">{{.Reply}}</td>
                <td>
                  <button class="btn btn-danger btn-sm" data-name="{{.Name}}" data-action="deleteRule">Delete</button>
                </td>
              </tr>
              {{end}}
            </tbody>
          </table>
        </div>
      </div>
    </div>

    <!-- TAB: WEBHOOKS & API (S8, D11) -->
    <div id="tab-webhooks" class="tab-content">
      <div class="card">
        <div class="card-title"><svg class="icon" viewBox="0 0 24 24"><path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"></path><path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"></path></svg> Inbound Webhook</div>
        <div style="background: #f8fafc; border: 1px solid var(--border); border-radius: 10px; padding: 16px; margin-bottom: 16px;">
          <div style="font-size: 11px; text-transform: uppercase; color: var(--text-muted); font-weight: 600; margin-bottom: 6px;">Endpoint URL</div>
          <div style="display: flex; align-items: center; justify-content: space-between; gap: 10px; background: #ffffff; border: 1px solid #cbd5e1; border-radius: 8px; padding: 10px 14px; font-family: monospace; font-size: 13px; color: #059669; font-weight: 600;">
            <span id="inboundWebhookUrl">POST http://127.0.0.1:{{.ServerPort}}/api/v1/webhook/send</span>
            <button type="button" class="btn btn-secondary btn-sm" data-action="copyToClipboard" data-copy-text="http://127.0.0.1:{{.ServerPort}}/api/v1/webhook/send" data-success-msg="Webhook URL copied!"><svg class="icon-sm" viewBox="0 0 24 24"><rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path></svg> Copy URL</button>
          </div>
          <div style="display: flex; align-items: center; justify-content: space-between; gap: 10px; background: #ffffff; border: 1px solid #cbd5e1; border-radius: 8px; padding: 10px 14px; font-family: monospace; font-size: 13px; color: #334155; margin-top: 10px;">
            <span>Header: <strong>X-Webhook-Secret:</strong> <code style="color: #0284c7;">{{.Webhook.Secret}}</code></span>
            <button type="button" class="btn btn-secondary btn-sm" data-action="copyToClipboard" data-copy-text="{{.Webhook.Secret}}" data-success-msg="Secret key copied!"><svg class="icon-sm" viewBox="0 0 24 24"><rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path></svg> Copy Secret</button>
          </div>
        </div>
      </div>

      <div class="card">
        <div class="card-title"><svg class="icon" viewBox="0 0 24 24"><circle cx="12" cy="12" r="2"></circle><path d="M16.24 7.76a6 6 0 0 1 0 8.49m-8.48-.01a6 6 0 0 1 0-8.49m11.31-2.82a10 10 0 0 1 0 14.14m-14.14 0a10 10 0 0 1 0-14.14"></path></svg> Outbound Webhook</div>
        <form id="webhookConfigForm">
          <div class="form-group">
            <label for="whURL">Outbound Destination URL</label>
            <input type="text" id="whURL" value="{{.Webhook.URL}}" placeholder="https://your-crm.com/api/whatsapp-events">
          </div>
          <div class="form-group">
            <label for="whSecret">Secret Key (HMAC-SHA256)</label>
            <input type="text" id="whSecret" value="{{.Webhook.Secret}}" placeholder="Secret string for signing">
          </div>
          <div style="display: flex; gap: 10px;">
            <button type="submit" class="btn btn-primary">Save Settings</button>
            <button type="button" class="btn btn-secondary" data-action="testWebhookPing">Test Ping</button>
          </div>
        </form>
      </div>
    </div>

    <!-- TAB: SETTINGS & SECURITY (S2, S11, C7, C12, D12) -->
    <div id="tab-settings" class="tab-content">
      <div class="grid-2">
        <!-- Password Security (S2) -->
        <div class="card">
          <div class="card-title"><svg class="icon" viewBox="0 0 24 24"><rect x="3" y="11" width="18" height="11" rx="2" ry="2"></rect><path d="M7 11V7a5 5 0 0 1 10 0v4"></path></svg> Master Admin Password</div>
          <form id="changePasswordForm">
            <div class="form-group">
              <label for="cpCurrent">Current Master Password</label>
              <input type="password" id="cpCurrent" placeholder="Enter current password" required>
            </div>
            <div class="form-group">
              <label for="cpNew">New Master Password</label>
              <input type="password" id="cpNew" placeholder="Minimum 8 characters (letters & numbers)" minlength="8" required>
            </div>

            <button type="submit" class="btn btn-primary">Update Password</button>
          </form>
        </div>

        <!-- Business Hours (D12) -->
        <div class="card">
          <div class="card-title"><svg class="icon" viewBox="0 0 24 24"><path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"></path></svg> Business Hours &amp; Timezone</div>
          <form id="businessHoursForm">
            <div class="form-group">
              <label for="bhTimezone">Operating Timezone</label>
              <input type="text" id="bhTimezone" value="{{.BusinessHours.Timezone}}" placeholder="Local, UTC, America/New_York, Asia/Kolkata">
            </div>
            <div class="grid-2">
              <div class="form-group">
                <label for="bhStart">Start Hour (0-23)</label>
                <input type="text" id="bhStart" value="{{.BusinessHours.StartHour}}">
              </div>
              <div class="form-group">
                <label for="bhEnd">End Hour (0-23)</label>
                <input type="text" id="bhEnd" value="{{.BusinessHours.EndHour}}">
              </div>
            </div>
            <div class="form-group">
              <label for="bhAway">Away Reply Template</label>
              <textarea id="bhAway" rows="2">{{.BusinessHours.AwayReply}}</textarea>
            </div>
            <button type="submit" class="btn btn-primary">Save Business Hours</button>
          </form>
        </div>
      </div>

      <!-- Google Gemini AI Bot (C7) -->
      <div class="card">
        <div class="card-title">
          <span style="display: inline-flex; align-items: center; gap: 8px;"><svg class="icon" viewBox="0 0 24 24"><path d="M12 3l1.912 5.885L19.798 10.8 13.912 12.715 12 18.6l-1.912-5.885L4.202 10.8l5.886-1.915L12 3z"></path></svg> Google Gemini AI Assistant</span>
          <a href="https://aistudio.google.com/app/apikey" target="_blank" rel="noopener noreferrer" style="font-size: 12px; color: #10b981; text-decoration: none; font-weight: 600; display: inline-flex; align-items: center; gap: 4px;">Get API Key <svg class="icon-sm" viewBox="0 0 24 24"><path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"></path><polyline points="15 3 21 3 21 9"></polyline><line x1="10" y1="14" x2="21" y2="3"></line></svg></a>
        </div>
        <form id="aiConfigForm">
          <div style="display: flex; align-items: center; gap: 12px; margin-bottom: 16px; padding: 12px 14px; background: #f0fdf4; border: 1px solid #bbf7d0; border-radius: 8px;">
            <input type="checkbox" id="aiEnabled" {{if .AI.Enabled}}checked{{end}} style="width: 18px; height: 18px; accent-color: #10b981; cursor: pointer;">
            <div>
              <label for="aiEnabled" style="font-weight: 600; font-size: 14px; margin: 0; cursor: pointer; color: #0f172a; text-transform: none;">Enable Gemini AI Smart Replies</label>
              <div style="font-size: 12px; color: var(--text-muted); margin-top: 2px;">When messages don't match any keyword rule, Gemini AI automatically crafts a smart, conversational response.</div>
            </div>
          </div>
          <div class="grid-2">
            <div class="form-group">
              <label for="aiKey">Gemini API Key</label>
              <div style="display: flex; gap: 6px;">
                <input type="password" id="aiKey" value="" placeholder="Enter Gemini API Key (e.g. AIzaSy...)" style="flex: 1;" autocomplete="off" autocapitalize="off" spellcheck="false">
                <button type="button" class="btn btn-secondary btn-sm" data-action="togglePasswordVisibility" data-field="aiKey" title="Show/Hide Key"><svg class="icon-sm" viewBox="0 0 24 24"><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"></path><circle cx="12" cy="12" r="3"></circle></svg></button>
              </div>
              <div style="font-size: 11px; color: var(--text-muted); margin-top: 4px;">Configure your Google Gemini API key for automated conversational replies.</div>
            </div>
            <div class="form-group">
              <label for="aiModel">Model Name</label>
              <select id="aiModel">
                <option value="gemini-3.8-flash" {{if or (eq .AI.Model "gemini-3.8-flash") (eq .AI.Model "") (eq .AI.Model "gemini-2.5-flash")}}selected{{end}}>gemini-3.8-flash (Latest, Recommended &amp; Fast)</option>
                <option value="gemini-3.7-flash" {{if eq .AI.Model "gemini-3.7-flash"}}selected{{end}}>gemini-3.7-flash (Multimodal &amp; Agentic)</option>
                <option value="gemini-3.5-flash-lite" {{if eq .AI.Model "gemini-3.5-flash-lite"}}selected{{end}}>gemini-3.5-flash-lite (Lowest Cost &amp; High Speed)</option>
              </select>
            </div>
          </div>
          <div class="form-group">
            <label for="aiPrompt">System Persona Instructions</label>
            <textarea id="aiPrompt" rows="3" placeholder="You are a friendly customer support assistant. Answer concisely in 1-3 sentences with emojis.">{{.AI.SystemPrompt}}</textarea>
          </div>
          <div style="display: flex; gap: 10px; align-items: center; flex-wrap: wrap;">
            <button type="submit" id="btnSaveAI" class="btn btn-primary">Save AI Settings</button>
            <button type="button" id="btnTestAI" class="btn btn-secondary" data-action="testGeminiApiKey"><svg class="icon" viewBox="0 0 24 24"><polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2"></polygon></svg> Test API Key</button>
            <span id="aiTestResult" style="font-size: 13px; font-weight: 600; display: none;"></span>
          </div>
        </form>
      </div>

      <!-- Data Retention & Privacy (C12) -->
      <div class="card">
        <div class="card-title"><svg class="icon" viewBox="0 0 24 24"><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"></path></svg> Data Retention &amp; Privacy</div>
        <div style="display: flex; gap: 12px; flex-wrap: wrap;">
          <button class="btn btn-secondary" data-action="exportPrivacyBundle"><svg class="icon-sm" viewBox="0 0 24 24"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"></path><polyline points="7 10 12 15 17 10"></polyline><line x1="12" y1="15" x2="12" y2="3"></line></svg> Export Data Bundle</button>
          <button class="btn btn-danger" data-action="purgeHistory" data-days="30">Purge Older than 30 Days</button>
          <button class="btn btn-danger" data-action="purgeHistory" data-days="90">Purge Older than 90 Days</button>
        </div>
      </div>

      <!-- Security Audit Trail (S11) -->
      <div class="card">
        <div class="card-title">
          <span style="display: inline-flex; align-items: center; gap: 8px;"><svg class="icon" viewBox="0 0 24 24"><path d="M16 4h2a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2h2"></path><rect x="8" y="2" width="8" height="4" rx="1" ry="1"></rect><path d="M9 12h6"></path><path d="M9 16h6"></path></svg> Security Audit Trail</span>
          <button class="btn btn-secondary btn-sm" data-action="loadAuditLog"><svg class="icon-sm" viewBox="0 0 24 24"><polyline points="23 4 23 10 17 10"></polyline><polyline points="1 20 1 14 7 14"></polyline><path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15"></path></svg> Refresh</button>
        </div>
        <div style="display: flex; gap: 12px; margin-bottom: 16px;">
          <input type="text" id="auditFilter" placeholder="Search audit trail..." oninput="filterAuditLive()" style="max-width: 360px;">
        </div>
        <div class="table-container">
          <table>
            <thead>
              <tr>
                <th>Timestamp</th>
                <th>Event Action</th>
                <th>User / IP</th>
                <th>Details</th>
                <th>Status</th>
              </tr>
            </thead>
            <tbody id="auditTableBody">
              <!-- Dynamically populated -->
            </tbody>
          </table>
        </div>
      </div>
    </div>
  </main>

  <!-- Confirm Modal (D6) - NO BACKDROP LEFT GUARANTEE -->
  <div id="confirmModal" class="modal-backdrop" data-close-backdrop="confirmModal">
    <div class="modal-card">
      <div class="modal-header">
        <div class="modal-title" id="confirmTitle">Confirm Action</div>
        <button class="modal-close" data-action="closeConfirmModal">&times;</button>
      </div>
      <div id="confirmMsg" style="font-size: 13px; color: var(--text-secondary); margin-bottom: 20px;"></div>
      <div style="display: flex; justify-content: flex-end; gap: 10px;">
        <button class="btn btn-secondary" data-action="closeConfirmModal">Cancel</button>
        <button class="btn btn-danger" id="confirmProceedBtn">Confirm</button>
      </div>
    </div>
  </div>

  <!-- Compliance Guide Modal (C1) - NO BACKDROP LEFT GUARANTEE -->
  <div id="complianceModal" class="modal-backdrop" data-close-backdrop="complianceModal">
    <div class="modal-card" style="max-width: 600px;">
      <div class="modal-header">
        <div class="modal-title"><svg class="icon" viewBox="0 0 24 24"><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"></path></svg> WhatsApp Anti-Ban Guidelines</div>
        <button class="modal-close" data-action="closeComplianceModal">&times;</button>
      </div>
      <div style="font-size: 13px; color: var(--text-secondary); line-height: 1.6; max-height: 420px; overflow-y: auto;">
        <h4 style="color: #0f172a; margin-bottom: 4px;">1. Explicit User Opt-In</h4>
        <p style="margin-bottom: 12px; color: var(--text-muted);">Never send unsolicited bulk messages. Only message customers who have explicitly opted in or initiated contact.</p>

        <h4 style="color: #0f172a; margin-bottom: 4px;">2. Smart STOP / Unsubscribe Support</h4>
        <p style="margin-bottom: 12px; color: var(--text-muted);">The platform automatically honors keywords: <code>STOP</code>, <code>UNSUBSCRIBE</code>, <code>CANCEL</code>, instantly toggling DND mode.</p>

        <h4 style="color: #0f172a; margin-bottom: 4px;">3. Warmup Schedule</h4>
        <p style="margin-bottom: 12px; color: var(--text-muted);">New numbers must be warmed up gradually: Day 1-3: 20-50 messages; Day 4-7: 50-100 messages. Avoid spiking volume abruptly.</p>

        <h4 style="color: #0f172a; margin-bottom: 4px;">4. Randomized Jitter Pacing</h4>
        <p style="margin-bottom: 12px; color: var(--text-muted);">Our broadcast engine uses 4-10 second randomized delays to simulate natural human typing and protect against automated rate flags.</p>

        <h4 style="color: #0f172a; margin-bottom: 4px;">5. Automatic Circuit Breaker</h4>
        <p style="color: var(--text-muted);">Broadcast campaigns automatically abort if 5 consecutive messages fail, preserving account health.</p>
      </div>
      <div style="text-align: right; margin-top: 18px;">
        <button class="btn btn-primary" data-action="closeComplianceModal">Understood</button>
      </div>
    </div>
  </div>

  <!-- Schedule Message Modal - NO BACKDROP LEFT GUARANTEE -->
  <div id="scheduleModal" class="modal-backdrop" data-close-backdrop="scheduleModal">
    <div class="modal-card">
      <div class="modal-header">
        <div class="modal-title"><svg class="icon" viewBox="0 0 24 24"><circle cx="12" cy="12" r="10"></circle><polyline points="12 6 12 12 16 14"></polyline></svg> Schedule Message</div>
        <button class="modal-close" data-action="closeScheduleModal">&times;</button>
      </div>
      <form id="scheduleForm">
        <div class="form-group">
          <label for="schRecipient">Recipient Phone Number (with Country Code)</label>
          <input type="text" id="schRecipient" placeholder="+919876543210 or +14155552671" required>
        </div>
        <div class="form-group">
          <label for="schName">Recipient Name (Optional)</label>
          <input type="text" id="schName" placeholder="e.g. John Doe">
        </div>
        <div class="form-group">
          <label for="schDateTime">Scheduled Date &amp; Time</label>
          <input type="datetime-local" id="schDateTime" required>
        </div>
        <div class="form-group">
          <label for="schMessage">Message (Supports &#123;&#123;name&#125;&#125;, &#123;&#123;greeting&#125;&#125;, &#123;&#123;first_name&#125;&#125;)</label>
          <textarea id="schMessage" rows="3" placeholder="Hello &#123;&#123;name&#125;&#125;! This is an automated reminder..." required></textarea>
        </div>
        <div style="display: flex; justify-content: flex-end; gap: 10px; margin-top: 16px;">
          <button type="button" class="btn btn-secondary" data-action="closeScheduleModal">Cancel</button>
          <button type="submit" class="btn btn-primary" id="btnSaveSchedule">Schedule Message</button>
        </div>
      </form>
    </div>
  </div>

  <!-- Contact Modal - NO BACKDROP LEFT GUARANTEE -->
  <div id="contactModal" class="modal-backdrop" data-close-backdrop="contactModal">
    <div class="modal-card">
      <div class="modal-header">
        <div class="modal-title"><svg class="icon" viewBox="0 0 24 24"><path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"></path><circle cx="9" cy="7" r="4"></circle><path d="M23 21v-2a4 4 0 0 0-3-3.87"></path><path d="M16 3.13a4 4 0 0 1 0 7.75"></path></svg> Add New Contact</div>
        <button class="modal-close" data-action="closeContactModal">&times;</button>
      </div>
      <form id="contactForm">
        <div class="form-group">
          <label for="ctPhone">Phone Number with Country Code</label>
          <input type="text" id="ctPhone" placeholder="+919876543210 or +14155552671" required>
        </div>
        <div class="form-group">
          <label for="ctName">Contact Full Name</label>
          <input type="text" id="ctName" placeholder="e.g. John Smith" required>
        </div>
        <div class="form-group">
          <label for="ctEmail">Email Address (Optional)</label>
          <input type="email" id="ctEmail" placeholder="john@example.com">
        </div>
        <div class="form-group">
          <label for="ctTags">Tags (Comma-separated, e.g. Customer, VIP)</label>
          <input type="text" id="ctTags" placeholder="Customer, VIP, Lead">
        </div>
        <div class="form-group">
          <label for="ctNotes">Notes (Optional)</label>
          <textarea id="ctNotes" rows="2" placeholder="Customer requirements, details..."></textarea>
        </div>
        <div style="display: flex; justify-content: flex-end; gap: 10px; margin-top: 16px;">
          <button type="button" class="btn btn-secondary" data-action="closeContactModal">Cancel</button>
          <button type="submit" class="btn btn-primary" id="btnSaveContact">Save Contact</button>
        </div>
      </form>
    </div>
  </div>

  <!-- Template Modal - NO BACKDROP LEFT GUARANTEE -->
  <div id="templateModal" class="modal-backdrop" data-close-backdrop="templateModal">
    <div class="modal-card">
      <div class="modal-header">
        <div class="modal-title"><svg class="icon" viewBox="0 0 24 24"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"></path><polyline points="14 2 14 8 20 8"></polyline><line x1="16" y1="13" x2="8" y2="13"></line><line x1="16" y1="17" x2="8" y2="17"></line><polyline points="10 9 9 9 8 9"></polyline></svg> Create Template</div>
        <button class="modal-close" data-action="closeTemplateModal">&times;</button>
      </div>
      <form id="templateForm">
        <div class="form-group">
          <label for="tmplName">Template Name</label>
          <input type="text" id="tmplName" placeholder="e.g. Welcome Onboarding" required>
        </div>
        <div class="form-group">
          <label for="tmplCategory">Category</label>
          <select id="tmplCategory">
            <option value="General">General</option>
            <option value="Marketing">Marketing</option>
            <option value="Support">Support</option>
            <option value="Transactional">Transactional</option>
            <option value="Follow-up">Follow-up</option>
          </select>
        </div>
        <div class="form-group">
          <label for="tmplContent">Template Content (Tokens: &#123;&#123;greeting&#125;&#125;, &#123;&#123;name&#125;&#125;, &#123;&#123;first_name&#125;&#125;, &#123;&#123;phone&#125;&#125;, &#123;&#123;date&#125;&#125;, &#123;&#123;time&#125;&#125;)</label>
          <textarea id="tmplContent" rows="4" placeholder="&#123;&#123;greeting&#125;&#125; &#123;&#123;name&#125;&#125;! Welcome to our service..." required></textarea>
        </div>
        <div style="display: flex; justify-content: flex-end; gap: 10px; margin-top: 16px;">
          <button type="button" class="btn btn-secondary" data-action="closeTemplateModal">Cancel</button>
          <button type="submit" class="btn btn-primary" id="btnSaveTemplate">Save Template</button>
        </div>
      </form>
    </div>
  </div>

  <!-- Rule Modal - NO BACKDROP LEFT GUARANTEE -->
  <div id="ruleModal" class="modal-backdrop" data-close-backdrop="ruleModal">
    <div class="modal-card">
      <div class="modal-header">
        <div class="modal-title"><svg class="icon" viewBox="0 0 24 24"><polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2"></polygon></svg> Create Auto-Reply Rule</div>
        <button class="modal-close" data-action="closeRuleModal">&times;</button>
      </div>
      <form id="ruleForm">
        <div class="form-group">
          <label for="ruleName">Rule Identifier / Name</label>
          <input type="text" id="ruleName" placeholder="e.g. pricing_inquiry" required>
        </div>
        <div class="grid-2">
          <div class="form-group">
            <label for="ruleType">Match Type</label>
            <select id="ruleType">
              <option value="exact">Exact Word / Phrase</option>
              <option value="contains">Contains Keyword</option>
              <option value="prefix">Starts With Prefix</option>
              <option value="regex">Regular Expression (Regex)</option>
            </select>
          </div>
          <div class="form-group">
            <label for="rulePattern">Pattern / Keywords (comma-separated)</label>
            <input type="text" id="rulePattern" placeholder="e.g. price, pricing, cost" required>
          </div>
        </div>
        <div class="form-group">
          <label for="ruleReply">Automated Reply Message (Supports tokens: &#123;&#123;greeting&#125;&#125;, &#123;&#123;name&#125;&#125;, &#123;&#123;first_name&#125;&#125;)</label>
          <textarea id="ruleReply" rows="3" placeholder="Hello &#123;&#123;first_name&#125;&#125;! Our pricing starts at..." required></textarea>
        </div>
        <div class="form-group">
          <label for="ruleDesc">Description (Optional)</label>
          <input type="text" id="ruleDesc" placeholder="Triggers when customer asks about pricing">
        </div>
        <div style="display: flex; justify-content: flex-end; gap: 10px; margin-top: 16px;">
          <button type="button" class="btn btn-secondary" data-action="closeRuleModal">Cancel</button>
          <button type="submit" class="btn btn-primary" id="btnSaveRule">Save Rule</button>
        </div>
      </form>
    </div>
  </div>
  <script src="/static/app.js" data-safe-select="selectChat(this.getAttribute)" defer></script>
</body>
</html>`
