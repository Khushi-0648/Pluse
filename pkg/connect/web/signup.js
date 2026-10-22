(() => {
  const cfg = window.PULSE_CONFIG || {};
  const btn = document.getElementById('fb-btn');
  const statusBox = document.getElementById('status-box');

  function showStatus(msg, type) {
    statusBox.textContent = msg;
    statusBox.className = 'status-box ' + (type || 'error');
  }

  window.fbAsyncInit = function() {
    FB.init({
      appId: cfg.appId,
      autoLogAppEvents: true,
      xfbml: false,
      version: cfg.version || 'v23.0'
    });
  };

  const script = document.createElement('script');
  script.src = cfg.sdkUrl || 'https://connect.facebook.net/en_US/sdk.js';
  script.async = true;
  script.defer = true;
  document.head.appendChild(script);

  if (btn) {
    btn.addEventListener('click', () => {
      btn.disabled = true;
      if (typeof FB === 'undefined') {
        showStatus('Facebook SDK is still loading. Please try again in a few seconds.', 'error');
        btn.disabled = false;
        return;
      }
      FB.login((response) => {
        if (!response.authResponse) {
          showStatus('Sign-up was cancelled or blocked.', 'error');
          btn.disabled = false;
          return;
        }
        const auth = response.authResponse;
        const code = auth.code;
        // Parse WhatsApp Embedded Signup session data from postMessage
      }, {
        config_id: cfg.configId,
        response_type: 'code',
        override_default_response_type: true,
        extras: {
          setup: {},
          featureType: '',
          sessionInfoVersion: '3'
        }
      });
    });
  }
})();
