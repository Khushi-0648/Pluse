package server

// dashboardJS is the external client-side application bundle for the Web Dashboard.
// Served at /static/app.js with Cache-Control and MIME application/javascript to eliminate
// inline script execution and enforce strict CSP (S12 & D2).
const dashboardJS = `    // Security Helpers: CSRF and HTML Escaping (S1, S6)
    function escapeHtml(str) {
      if (!str) return '';
      return String(str)
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;')
        .replace(/'/g, '&#39;');
    }

    function getCsrfToken() {
      const match = document.cookie.match(/(^|;)\s*wa_csrf=([^;]+)/);
      return match ? match[2] : '';
    }

    async function apiFetch(url, options = {}) {
      options.headers = options.headers || {};
      const method = (options.method || 'GET').toUpperCase();
      if (['POST', 'PUT', 'DELETE', 'PATCH'].includes(method)) {
        options.headers['X-CSRF-Token'] = getCsrfToken();
      }
      try {
        const res = await fetch(url, options);
        return res;
      } catch (err) {
        showToast('Network error: ' + err.message, 'error');
        throw err;
      }
    }

    // Modern Toast Notification System (D6)
    function showToast(message, type = 'info') {
      const container = document.getElementById('toast-container');
      const toast = document.createElement('div');
      toast.className = 'toast ' + type;
      toast.innerHTML = '<span>' + escapeHtml(message) + '</span>';
      container.appendChild(toast);
      setTimeout(() => {
        toast.style.opacity = '0';
        toast.style.transform = 'translateY(10px)';
        toast.style.transition = 'all 0.25s ease';
        setTimeout(() => toast.remove(), 250);
      }, 4000);
    }

    // Modern Modal Confirm System (D6)
    let pendingConfirmAction = null;
    function showConfirm(title, message, onConfirm) {
      document.getElementById('confirmTitle').innerText = title;
      document.getElementById('confirmMsg').innerText = message;
      pendingConfirmAction = onConfirm;
      document.getElementById('confirmModal').classList.add('open');
    }
    function closeConfirmModal() {
      document.getElementById('confirmModal').classList.remove('open');
      pendingConfirmAction = null;
    }
    document.getElementById('confirmProceedBtn').addEventListener('click', () => {
      if (pendingConfirmAction) pendingConfirmAction();
      closeConfirmModal();
    });

    // Zero-Backdrop System: Clean Dismissal for All Modals
    function closeAllModals() {
      document.querySelectorAll('.modal-backdrop').forEach(m => m.classList.remove('open'));
      pendingConfirmAction = null;
    }
    window.addEventListener('keydown', (e) => {
      if (e.key === 'Escape') closeAllModals();
    });

    // One-Click Clipboard Copy Helper
    function copyToClipboard(text, successMsg) {
      if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(text).then(() => {
          showToast(successMsg || 'Copied to clipboard!', 'success');
        }).catch(() => {
          fallbackCopyText(text, successMsg);
        });
      } else {
        fallbackCopyText(text, successMsg);
      }
    }

    function fallbackCopyText(text, successMsg) {
      const ta = document.createElement('textarea');
      ta.value = text;
      ta.style.position = 'fixed';
      ta.style.opacity = '0';
      document.body.appendChild(ta);
      ta.focus();
      ta.select();
      try {
        document.execCommand('copy');
        showToast(successMsg || 'Copied to clipboard!', 'success');
      } catch (e) {
        showToast('Unable to copy text: ' + e.message, 'error');
      }
      document.body.removeChild(ta);
    }

    // Live Table Filtering Enhancements
    function filterScheduledLive() {
      const q = (document.getElementById('schFilter')?.value || '').toLowerCase().trim();
      const rows = document.querySelectorAll('#scheduledTableBody tr');
      rows.forEach(r => {
        if (!q) {
          r.style.display = '';
          return;
        }
        const text = r.innerText.toLowerCase();
        r.style.display = text.includes(q) ? '' : 'none';
      });
    }

    function filterTemplatesLive() {
      const q = (document.getElementById('tmplFilter')?.value || '').toLowerCase().trim();
      const rows = document.querySelectorAll('#templatesTableBody tr');
      rows.forEach(r => {
        if (!q) {
          r.style.display = '';
          return;
        }
        const text = r.innerText.toLowerCase();
        r.style.display = text.includes(q) ? '' : 'none';
      });
    }

    function filterRulesLive() {
      const q = (document.getElementById('ruleFilter')?.value || '').toLowerCase().trim();
      const rows = document.querySelectorAll('#rulesTableBody tr');
      rows.forEach(r => {
        if (!q) {
          r.style.display = '';
          return;
        }
        const text = r.innerText.toLowerCase();
        r.style.display = text.includes(q) ? '' : 'none';
      });
    }

    function filterAuditLive() {
      const q = (document.getElementById('auditFilter')?.value || '').toLowerCase().trim();
      const rows = document.querySelectorAll('#auditTableBody tr');
      rows.forEach(r => {
        if (!q) {
          r.style.display = '';
          return;
        }
        const text = r.innerText.toLowerCase();
        r.style.display = text.includes(q) ? '' : 'none';
      });
    }

    function userLogout() {
      showConfirm('Log Out', 'Are you sure you want to log out? Logging out will unlink your WhatsApp device from your phone, and clear your session, contacts, and chat history.', () => {
        try {
          localStorage.clear();
          sessionStorage.clear();
        } catch (e) {}
        window.location.href = '/logout';
      });
    }

    // Navigation and Tab Switching (X11)
    document.querySelectorAll('.nav-link').forEach(link => {
      link.addEventListener('click', (e) => {
        e.preventDefault();
        const tab = link.getAttribute('data-tab');
        if (!tab) return;
        switchTab(tab);
      });
    });

    function switchTab(tabId) {
      closeAllModals();
      document.querySelectorAll('.nav-link').forEach(l => l.classList.remove('active'));
      document.querySelectorAll('.tab-content').forEach(c => c.classList.remove('active'));
      const activeLink = document.querySelector('.nav-link[data-tab="' + tabId + '"]');
      const activeContent = document.getElementById('tab-' + tabId);
      if (activeLink) activeLink.classList.add('active');
      if (activeContent) activeContent.classList.add('active');

      const titles = {
        dashboard: 'Dashboard',
        messages: 'Team Inbox',
        scheduled: 'Scheduled',
        contacts: 'Contacts',
        broadcast: 'Broadcasts',
        templates: 'Templates',
        rules: 'Auto-Reply Rules',
        webhooks: 'Webhooks & API',
        settings: 'Settings'
      };
      document.getElementById('pageTitle').innerText = titles[tabId] || 'Web Auto';
      localStorage.setItem('activePulseTab', tabId);

      if (tabId === 'messages') loadInbox();
      if (tabId === 'contacts') loadContacts();
      if (tabId === 'settings') loadAuditLog();
    }

    function toggleSidebar() {
      document.getElementById('sidebar').classList.toggle('open');
    }

    // Anti-Ban Modal
    function openComplianceModal() { document.getElementById('complianceModal').classList.add('open'); }
    function closeComplianceModal() { document.getElementById('complianceModal').classList.remove('open'); }

    // Quick Sender
    document.getElementById('quickSendForm').addEventListener('submit', async (e) => {
      e.preventDefault();
      const btn = document.getElementById('qsBtn');
      btn.disabled = true;
      btn.innerText = 'Sending...';

      const phone = document.getElementById('qsPhone').value;
      const name = document.getElementById('qsName').value;
      const message = document.getElementById('qsMsg').value;
      const fileInput = document.getElementById('qsFile');

      try {
        let res;
        if (fileInput.files.length > 0) {
          const formData = new FormData();
          formData.append('to', phone);
          formData.append('name', name);
          formData.append('message', message);
          formData.append('media', fileInput.files[0]);
          res = await apiFetch('/api/v1/messages/send', { method: 'POST', body: formData });
        } else {
          res = await apiFetch('/api/v1/messages/send', {
            method: 'POST',
            headers: {'Content-Type': 'application/json'},
            body: JSON.stringify({ to: phone, name: name, message: message })
          });
        }

        const data = await res.json();
        if (res.ok) {
          showToast('Message sent successfully!', 'success');
          document.getElementById('qsPhone').value = '';
          document.getElementById('qsName').value = '';
          document.getElementById('qsMsg').value = '';
          fileInput.value = '';
        } else {
          showToast(data.error || 'Failed to send message', 'error');
        }
      } catch (err) {
        showToast('Error: ' + err.message, 'error');
      } finally {
        btn.disabled = false;
        btn.innerText = 'Send Message';
      }
    });

    // Two-Way Team Inbox Logic (C5, S1, S4)
    let currentChatPhone = '';
    let currentChatName = '';
    let inboxMessages = [];

    let cachedContacts = [];

    async function loadInbox() {
      try {
        const [msgRes, contactsRes] = await Promise.all([
          apiFetch('/api/v1/messages?limit=100'),
          apiFetch('/api/v1/contacts').catch(() => null)
        ]);
        if (!msgRes.ok) return;
        inboxMessages = await msgRes.json() || [];
        if (contactsRes && contactsRes.ok) {
          cachedContacts = await contactsRes.json() || [];
        }
        renderInboxContacts();
        if (currentChatPhone) renderChatStream(currentChatPhone);
      } catch (e) {
        console.error(e);
      }
    }

    function isLIDOrPseudoName(str) {
      if (!str) return true;
      const clean = str.replace(/^\+/, '').trim();
      if (!clean || clean === '-') return true;
      if (/^\d{13,18}$/.test(clean)) return true;
      return false;
    }

    function renderInboxContacts() {
      const list = document.getElementById('inboxContactsList');
      if (!list) return;

      const searchInp = document.getElementById('inboxSearchInput');
      const query = (searchInp && searchInp.value) ? searchInp.value.toLowerCase().trim() : '';

      // Build contact lookup map from cachedContacts
      const crmMap = new Map();
      (cachedContacts || []).forEach(c => {
        const p = (c.phone || '').replace(/^\+/, '');
        if (p && c.name && !isLIDOrPseudoName(c.name)) {
          crmMap.set(p, c.name.trim());
        }
      });

      const contactMap = new Map();
      inboxMessages.forEach(m => {
        const rawPhone = (m.direction === 'incoming' ? m.sender_phone : m.recipient) || '';
        if (!rawPhone || rawPhone === 'Me') return;
        const phone = rawPhone.replace(/^\+/, '');

        let nameCandidate = '';
        if (crmMap.has(phone) && crmMap.get(phone)) {
          nameCandidate = crmMap.get(phone);
        } else if (m.sender_name && m.sender_name.trim() && !isLIDOrPseudoName(m.sender_name) && m.sender_name !== 'Direct Dispatch' && m.sender_name !== 'CRM Inbound API' && m.sender_name !== phone && m.sender_name !== ('+' + phone) && m.sender_name !== '-') {
          nameCandidate = m.sender_name.trim();
        }

        let existing = contactMap.get(phone);
        if (!existing) {
          contactMap.set(phone, {
            phone: phone,
            name: nameCandidate || ('+' + phone),
            lastMsg: m.text,
            timestamp: new Date(m.timestamp),
            isAuto: m.type === 'auto_reply'
          });
        } else {
          // If existing didn't have a real named contact and this message does, upgrade it
          const existingHasReal = existing.name && !isLIDOrPseudoName(existing.name) && existing.name !== ('+' + phone) && existing.name !== phone;
          if (!existingHasReal && nameCandidate) {
            existing.name = nameCandidate;
          }
        }
      });

      let items = Array.from(contactMap.values());
      if (query) {
        items = items.filter(c => (c.name && c.name.toLowerCase().includes(query)) || (c.phone && c.phone.includes(query)) || (c.lastMsg && c.lastMsg.toLowerCase().includes(query)));
      }

      if (items.length === 0) {
        list.innerHTML = '<li style="padding: 24px; text-align: center; color: var(--text-muted); font-size: 12px;">' + (query ? 'No matching conversations.' : 'No conversations logged yet.') + '</li>';
        return;
      }

      list.innerHTML = items.map(c => {
        const cleanPhone = c.phone.replace(/^\+/, '');
        const hasRealName = c.name && c.name.trim() && 
          c.name.trim() !== ('+' + cleanPhone) && 
          c.name.trim() !== cleanPhone && 
          c.name.trim() !== '-' && 
          !isLIDOrPseudoName(c.name.trim());
        const displayName = hasRealName ? c.name.trim() : ('+' + cleanPhone);
        const subPhone = '+' + cleanPhone;
        const initial = displayName.charAt(0).toUpperCase();
        const activeCls = cleanPhone === currentChatPhone.replace(/^\+/, '') ? 'active' : '';

        const crmContact = (cachedContacts || []).find(x => (x.phone || '').replace(/^\+/, '') === cleanPhone);
        const needsHandover = crmContact && (crmContact.needs_attention || (crmContact.bot_paused_until && new Date(crmContact.bot_paused_until) > new Date()));
        const handoverBadge = needsHandover ? '<span class="badge badge-warn" style="font-size: 10px; padding: 1px 5px; margin-left: 4px;">HANDOVER</span>' : '';

        return '<li class="inbox-item ' + activeCls + '" data-phone="' + escapeHtml(cleanPhone) + '" data-name="' + escapeHtml(displayName) + '" onclick="selectChat(this.getAttribute(\'data-phone\'), this.getAttribute(\'data-name\'))">' +
          '<div class="inbox-avatar">' + escapeHtml(initial) + '</div>' +
          '<div class="inbox-meta" style="overflow: hidden; width: 100%;">' +
            '<div class="inbox-name" style="display: flex; justify-content: space-between; align-items: baseline; gap: 6px;">' +
              '<span style="font-weight: 600; color: #0f172a; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;">' + escapeHtml(displayName) + handoverBadge + '</span>' +
              (hasRealName ? '<span style="font-size: 11px; font-family: monospace; color: var(--text-muted); flex-shrink: 0;">' + escapeHtml(subPhone) + '</span>' : '') +
            '</div>' +
            '<div class="inbox-last-msg">' + escapeHtml(c.lastMsg || '') + '</div>' +
          '</div>' +
        '</li>';
      }).join('');
    }

    function filterInboxContacts() {
      renderInboxContacts();
    }

    function selectChat(phone, name) {
      const cleanPhone = (phone || '').replace(/^\+/, '');
      let displayName = (name && name.trim()) ? name.trim() : '';
      if (!displayName || displayName === '-' || displayName === cleanPhone || displayName === ('+' + cleanPhone) || isLIDOrPseudoName(displayName)) {
        const found = (cachedContacts || []).find(c => (c.phone || '').replace(/^\+/, '') === cleanPhone);
        if (found && found.name && found.name.trim() && found.name.trim() !== cleanPhone && found.name.trim() !== ('+' + cleanPhone) && found.name.trim() !== '-' && !isLIDOrPseudoName(found.name)) {
          displayName = found.name.trim();
        } else {
          displayName = '+' + cleanPhone;
        }
      }
      currentChatPhone = cleanPhone;
      currentChatName = displayName;
      
      const nameEl = document.getElementById('chatContactName');
      const phoneEl = document.getElementById('chatContactPhone');
      if (nameEl) nameEl.innerText = currentChatName;
      if (phoneEl) {
        if (currentChatName !== ('+' + cleanPhone) && !isLIDOrPseudoName(currentChatName)) {
          phoneEl.innerText = '+' + cleanPhone;
          phoneEl.style.display = 'block';
        } else {
          phoneEl.style.display = 'none';
        }
      }
      
      const actControls = document.getElementById('chatActionControls');
      if (actControls) actControls.style.display = 'flex';

      const crmContact = (cachedContacts || []).find(x => (x.phone || '').replace(/^\+/, '') === cleanPhone);
      const needsHandover = crmContact && (crmContact.needs_attention || (crmContact.bot_paused_until && new Date(crmContact.bot_paused_until) > new Date()));
      const handoverControls = document.getElementById('chatHandoverControls');
      if (handoverControls) handoverControls.style.display = needsHandover ? 'flex' : 'none';

      renderChatStream(cleanPhone);
      renderInboxContacts();
    }

    async function resumeBotForCurrentChat() {
      if (!currentChatPhone) {
        showToast('No chat selected', 'warn');
        return;
      }
      try {
        const res = await apiFetch('/api/v1/contacts/resume-bot', {
          method: 'POST',
          headers: {'Content-Type': 'application/json'},
          body: JSON.stringify({ phone: currentChatPhone })
        });
        const d = await res.json();
        if (res.ok && d.success) {
          showToast('Auto-replies resumed for this contact', 'success');
          const hc = document.getElementById('chatHandoverControls');
          if (hc) hc.style.display = 'none';
          await loadContacts();
          await loadInbox();
        } else {
          showToast(d.error || 'Failed to resume bot', 'error');
        }
      } catch (err) {
        showToast('Error resuming bot: ' + err.message, 'error');
      }
    }

    let inboxSelectedFile = null;

    function handleInboxFileSelect(input) {
      if (input.files && input.files[0]) {
        const file = input.files[0];
        if (file.size > 16 * 1024 * 1024) {
          showToast('Attachment exceeds 16MB limit', 'error');
          input.value = '';
          return;
        }
        inboxSelectedFile = file;
        const badge = document.getElementById('inboxFileBadge');
        const nameSpan = document.getElementById('inboxFileName');
        if (badge) badge.style.display = 'flex';
        if (nameSpan) nameSpan.innerHTML = '<svg class="icon-sm" viewBox="0 0 24 24"><path d="M21.44 11.05l-9.19 9.19a6 6 0 0 1-8.49-8.49l9.19-9.19a4 4 0 0 1 5.66 5.66l-9.2 9.19a2 2 0 0 1-2.83-2.83l8.49-8.48"></path></svg> <span>' + escapeHtml(file.name) + ' (' + Math.round(file.size / 1024) + ' KB)</span>';
        showToast('Attachment selected: ' + file.name, 'info');
      }
    }

    function clearInboxFile() {
      inboxSelectedFile = null;
      const inp = document.getElementById('inboxFile');
      if (inp) inp.value = '';
      const badge = document.getElementById('inboxFileBadge');
      if (badge) badge.style.display = 'none';
    }

    function renderChatStream(phone) {
      const stream = document.getElementById('inboxChatStream');
      if (!stream) return;
      const cleanTarget = (phone || '').replace(/^\+/, '');
      const filtered = inboxMessages.filter(m => {
        const sPhone = (m.sender_phone || '').replace(/^\+/, '');
        const rPhone = (m.recipient || '').replace(/^\+/, '');
        return sPhone === cleanTarget || rPhone === cleanTarget;
      }).reverse();

      if (filtered.length === 0) {
        stream.innerHTML = '<div class="empty-state"><div class="empty-icon"><svg class="icon-xl" viewBox="0 0 24 24"><path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"></path></svg></div><div class="empty-text">No messages with this contact</div></div>';
        return;
      }

      const wasAtBottom = (stream.scrollHeight - stream.scrollTop - stream.clientHeight) < 60;

      stream.innerHTML = filtered.map(m => {
        const isInc = m.direction === 'incoming';
        const cls = isInc ? 'bubble-incoming' : 'bubble-outgoing';
        const d = new Date(m.timestamp);
        const timeStr = d.toLocaleTimeString([], {hour: '2-digit', minute: '2-digit'});
        return '<div class="bubble ' + cls + '">' +
          '<div>' + escapeHtml(m.text) + '</div>' +
          '<div class="bubble-time">' + timeStr + (isInc ? '' : ' <svg class="icon-sm" style="width: 12px; height: 12px; vertical-align: -1px;" viewBox="0 0 24 24"><polyline points="20 6 9 17 4 12"></polyline></svg>') + '</div>' +
        '</div>';
      }).join('');
      if (wasAtBottom) {
        stream.scrollTop = stream.scrollHeight;
      }
    }

    async function refreshInbox(showToastFeedback = false) {
      await loadInbox();
      if (showToastFeedback) {
        showToast('Inbox refreshed', 'info');
      }
    }

    async function refreshCurrentChat() {
      if (!currentChatPhone) {
        showToast('No chat selected', 'warn');
        return;
      }
      await loadInbox();
      renderChatStream(currentChatPhone);
      showToast('Chat refreshed', 'info');
    }

    function clearCurrentChat() {
      if (!currentChatPhone) {
        showToast('No chat selected to clear', 'warn');
        return;
      }
      const displayName = currentChatName || currentChatPhone;
      const cleanPhone = currentChatPhone.replace(/^\+/, '');
      showConfirm('Clear Chat History', 'Are you sure you want to delete all chat history with ' + displayName + ' (+' + cleanPhone + ')? This action cannot be undone.', async () => {
        try {
          const res = await apiFetch('/api/v1/messages?phone=' + encodeURIComponent(currentChatPhone), {
            method: 'DELETE'
          });
          if (res.ok) {
            showToast('Chat history cleared', 'success');
            await loadInbox();
            renderChatStream(currentChatPhone);
          } else {
            const err = await res.json();
            showToast(err.error || 'Failed to clear chat', 'error');
          }
        } catch (e) {
          showToast('Failed to clear chat: ' + e.message, 'error');
        }
      });
    }

    function clearAllInbox() {
      showConfirm('Clear All Inbox', 'Are you sure you want to permanently delete all messages and conversation history across all contacts? This cannot be undone.', async () => {
        try {
          const res = await apiFetch('/api/v1/messages', {
            method: 'DELETE'
          });
          if (res.ok) {
            showToast('All inbox messages cleared', 'success');
            currentChatPhone = '';
            currentChatName = '';
            document.getElementById('chatContactName').innerText = 'Select a chat to begin';
            document.getElementById('chatContactPhone').innerText = '-';
            const actControls = document.getElementById('chatActionControls');
            if (actControls) actControls.style.display = 'none';
            await loadInbox();
            const stream = document.getElementById('inboxChatStream');
            if (stream) {
              stream.innerHTML = '<div class="empty-state"><div class="empty-icon"><svg class="icon-xl" viewBox="0 0 24 24"><path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"></path></svg></div><div class="empty-text">No conversation selected</div><div class="empty-sub">Select a chat from the left pane or start a new chat from Contacts.</div></div>';
            }
          } else {
            const err = await res.json();
            showToast(err.error || 'Failed to clear inbox', 'error');
          }
        } catch (e) {
          showToast('Failed to clear inbox: ' + e.message, 'error');
        }
      });
    }

    function insertQuickReply(text) {
      const inp = document.getElementById('inboxInput');
      inp.value = text;
      inp.focus();
    }

    async function sendInboxMessage() {
      const inp = document.getElementById('inboxInput');
      const text = inp.value.trim();
      if (!text && !inboxSelectedFile) return;
      if (!currentChatPhone) {
        showToast('Please select a conversation first', 'warn');
        return;
      }

      inp.value = '';
      const fileToSend = inboxSelectedFile;
      clearInboxFile();

      try {
        if (fileToSend) {
          const reader = new FileReader();
          reader.onload = async () => {
            const base64Data = reader.result;
            try {
              const res = await apiFetch('/api/v1/messages/send', {
                method: 'POST',
                headers: {'Content-Type': 'application/json'},
                body: JSON.stringify({
                  to: currentChatPhone,
                  name: currentChatName,
                  message: text,
                  media_data: base64Data,
                  file_name: fileToSend.name,
                  mime_type: fileToSend.type
                })
              });
              if (res.ok) {
                showToast('Message & attachment sent', 'success');
                loadInbox();
              } else {
                const d = await res.json();
                showToast(d.error || 'Failed to send', 'error');
              }
            } catch (e) {
              showToast('Error sending message: ' + e.message, 'error');
            }
          };
          reader.readAsDataURL(fileToSend);
          return;
        }

        const res = await apiFetch('/api/v1/messages/send', {
          method: 'POST',
          headers: {'Content-Type': 'application/json'},
          body: JSON.stringify({ to: currentChatPhone, name: currentChatName, message: text })
        });
        if (res.ok) {
          showToast('Message sent', 'success');
          loadInbox();
        } else {
          const d = await res.json();
          showToast(d.error || 'Failed to send', 'error');
        }
      } catch (err) {
        showToast('Error sending message: ' + err.message, 'error');
      }
    }

    // Contacts CRM Loader (D3, S4, D7)
    async function loadContacts() {
      try {
        const res = await apiFetch('/api/v1/contacts');
        if (!res.ok) return;
        cachedContacts = await res.json() || [];
        renderContactsList(cachedContacts);
      } catch (e) {
        console.error(e);
      }
    }

    function filterContactsLive() {
      const q = (document.getElementById('contactFilter')?.value || '').toLowerCase().trim();
      if (!q) {
        renderContactsList(cachedContacts);
        return;
      }
      const filtered = (cachedContacts || []).filter(c => {
        const name = (c.name || '').toLowerCase();
        const phone = (c.phone || '').toLowerCase();
        const email = (c.email || '').toLowerCase();
        const tags = (c.tags || []).join(' ').toLowerCase();
        return name.includes(q) || phone.includes(q) || email.includes(q) || tags.includes(q);
      });
      renderContactsList(filtered);
    }

    function renderContactsList(contacts) {
      const tbody = document.getElementById('contactsTableBody');
      if (!tbody) return;

      const statusText = document.getElementById('statusText');
      const isConnected = statusText && statusText.innerText.toLowerCase().includes('connected');

      if (!isConnected) {
        tbody.innerHTML = '<tr><td colspan="6" style="text-align: center; color: var(--text-muted); padding: 48px 20px;">' +
          '<div style="margin-bottom: 12px;"><svg class="icon-xl" viewBox="0 0 24 24"><rect x="5" y="2" width="14" height="20" rx="2" ry="2"></rect><line x1="12" y1="18" x2="12.01" y2="18"></line></svg></div>' +
          '<div style="font-size: 16px; font-weight: 700; color: #334155; margin-bottom: 6px;">WhatsApp Device Not Connected</div>' +
          '<div style="font-size: 13px; color: #64748b; max-width: 500px; margin: 0 auto 16px auto;">Contacts are only visible for the currently logged-in WhatsApp device. Once logged out, previous session contacts are cleared. Scan the QR code on the Dashboard to connect.</div>' +
          '<button class="btn btn-primary btn-sm" onclick="switchTab(\'dashboard\')">Go to QR Pairing</button>' +
        '</td></tr>';
        return;
      }

      if (!contacts || contacts.length === 0) {
        tbody.innerHTML = '<tr><td colspan="6" style="text-align: center; color: var(--text-muted); padding: 32px;">No contacts found for this WhatsApp account. Click "+ Add Contact" or import a CSV.</td></tr>';
        return;
      }

      tbody.innerHTML = contacts.map(c => {
        const cleanPhone = (c.phone || '').replace(/^\+/, '');
        const hasRealName = c.name && c.name.trim() && c.name.trim() !== '-' && c.name.trim() !== cleanPhone && c.name.trim() !== ('+' + cleanPhone) && !isLIDOrPseudoName(c.name);
        const displayName = hasRealName ? c.name.trim() : ('+' + cleanPhone);
        const tagsHtml = (c.tags || []).map(t => '<span class="badge badge-in">' + escapeHtml(t) + '</span>').join(' ');
        const dndBadge = c.is_dnd ? '<span class="badge badge-danger">DND ACTIVE</span>' : '<span class="badge badge-out">SUBSCRIBED</span>';
        return '<tr>' +
          '<td><strong style="font-family: monospace; color: #0284c7;">+' + escapeHtml(cleanPhone) + '</strong></td>' +
          '<td><strong style="color: #0f172a;">' + escapeHtml(displayName) + '</strong></td>' +
          '<td>' + escapeHtml(c.email || '-') + '</td>' +
          '<td>' + (tagsHtml || '-') + '</td>' +
          '<td>' + dndBadge + '</td>' +
          '<td>' +
            '<div style="display: flex; gap: 6px;">' +
              '<button class="btn btn-secondary btn-sm" data-phone="' + escapeHtml(cleanPhone) + '" data-name="' + escapeHtml(displayName) + '" onclick="selectChat(this.getAttribute(\'data-phone\'), this.getAttribute(\'data-name\')); switchTab(\'messages\');"><svg class="icon-sm" viewBox="0 0 24 24"><path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"></path></svg> Chat</button>' +
              '<button class="btn btn-danger btn-sm" data-phone="' + escapeHtml(cleanPhone) + '" onclick="deleteContact(this.getAttribute(\'data-phone\'))">Delete</button>' +
            '</div>' +
          '</td>' +
        '</tr>';
      }).join('');
    }

    function deleteContact(phone) {
      showConfirm('Delete Contact', 'Are you sure you want to delete contact +' + phone + '?', async () => {
        try {
          const res = await apiFetch('/api/v1/contacts/' + encodeURIComponent(phone), { method: 'DELETE' });
          if (res.ok) {
            showToast('Contact deleted', 'success');
            loadContacts();
          } else {
            showToast('Failed to delete contact', 'error');
          }
        } catch (err) {
          showToast('Error deleting contact', 'error');
        }
      });
    }

    function deleteRule(name) {
      showConfirm('Delete Rule', 'Are you sure you want to delete rule "' + name + '"?', async () => {
        try {
          const res = await apiFetch('/api/v1/rules/' + encodeURIComponent(name), { method: 'DELETE' });
          if (res.ok) {
            showToast('Rule deleted', 'success');
            location.reload();
          }
        } catch (e) {
          showToast('Error deleting rule', 'error');
        }
      });
    }

    function deleteTemplate(id) {
      showConfirm('Delete Template', 'Delete template "' + id + '"?', async () => {
        try {
          const res = await apiFetch('/api/v1/templates/' + encodeURIComponent(id), { method: 'DELETE' });
          if (res.ok) {
            showToast('Template deleted', 'success');
            location.reload();
          }
        } catch (e) {
          showToast('Error deleting template', 'error');
        }
      });
    }

    function deleteScheduled(id) {
      showConfirm('Cancel Scheduled Task', 'Cancel this scheduled message?', async () => {
        try {
          const res = await apiFetch('/api/v1/scheduled/' + encodeURIComponent(id) + '?permanent=true', { method: 'DELETE' });
          if (res.ok) {
            showToast('Scheduled task cancelled', 'success');
            location.reload();
          }
        } catch (e) {
          showToast('Error cancelling scheduled task', 'error');
        }
      });
    }

    async function retryScheduled(id) {
      if (!id) return;
      try {
        const res = await apiFetch('/api/v1/scheduled/' + encodeURIComponent(id), { method: 'POST' });
        const d = await res.json();
        if (res.ok && d.success) {
          showToast('Scheduled task reset to pending for dispatch!', 'success');
          location.reload();
        } else {
          showToast(d.error || 'Failed to retry scheduled task', 'error');
        }
      } catch (err) {
        showToast('Error retrying task: ' + err.message, 'error');
      }
    }

    // Modal Control Functions
    function openScheduleModal() {
      const dt = document.getElementById('schDateTime');
      if (dt) {
        const now = new Date(Date.now() + 3600000);
        now.setMinutes(now.getMinutes() - now.getTimezoneOffset());
        dt.value = now.toISOString().slice(0, 16);
        dt.min = new Date().toISOString().slice(0, 16);
      }
      document.getElementById('scheduleModal').classList.add('open');
    }
    function closeScheduleModal() {
      document.getElementById('scheduleModal').classList.remove('open');
      const f = document.getElementById('scheduleForm');
      if (f) f.reset();
    }

    function openContactModal() {
      document.getElementById('contactModal').classList.add('open');
      const p = document.getElementById('ctPhone');
      if (p) p.focus();
    }
    function closeContactModal() {
      document.getElementById('contactModal').classList.remove('open');
      const f = document.getElementById('contactForm');
      if (f) f.reset();
    }

    function openTemplateModal() {
      document.getElementById('templateModal').classList.add('open');
      const n = document.getElementById('tmplName');
      if (n) n.focus();
    }
    function closeTemplateModal() {
      document.getElementById('templateModal').classList.remove('open');
      const f = document.getElementById('templateForm');
      if (f) f.reset();
    }

    function openRuleModal() {
      document.getElementById('ruleModal').classList.add('open');
      const n = document.getElementById('ruleName');
      if (n) n.focus();
    }
    function closeRuleModal() {
      document.getElementById('ruleModal').classList.remove('open');
      const f = document.getElementById('ruleForm');
      if (f) f.reset();
    }

    // Broadcast Campaign Helpers
    async function applyTemplateToBroadcast(tmplId) {
      const msgBox = document.getElementById('bcMessage');
      if (!msgBox) return;
      if (!tmplId) return;
      try {
        const res = await apiFetch('/api/v1/templates');
        if (res.ok) {
          const tmpls = await res.json();
          const found = (tmpls || []).find(t => t.id === tmplId);
          if (found && found.content) {
            msgBox.value = found.content;
            showToast('Template applied: ' + found.name, 'info');
          }
        }
      } catch (e) {
        console.error(e);
      }
    }

    let broadcastPollTimer = null;

    async function pollBroadcastProgress() {
      try {
        const res = await apiFetch('/api/v1/campaigns/status');
        if (!res.ok) return;
        const d = await res.json();
        const box = document.getElementById('broadcastProgressBox');
        const bar = document.getElementById('bcProgressBar');
        const stats = document.getElementById('bcProgressStats');
        const startBtn = document.getElementById('startBroadcastBtn');

        if (d.is_broadcasting || d.status === 'running') {
          if (box) box.style.display = 'block';
          if (startBtn) {
            startBtn.disabled = true;
            startBtn.innerText = 'Broadcast in progress...';
          }
          const pct = d.total > 0 ? Math.round((d.current / d.total) * 100) : 0;
          if (bar) bar.style.width = pct + '%';
          if (stats) stats.innerText = d.current + ' / ' + d.total + ' (' + d.success + ' sent, ' + d.failed + ' failed)';
        } else {
          if (d.status === 'completed' || d.status === 'cancelled') {
            if (bar) bar.style.width = '100%';
            if (stats) stats.innerText = d.total + ' / ' + d.total + ' (' + d.success + ' sent, ' + d.failed + ' failed) - ' + (d.status ? d.status.toUpperCase() : '');
            if (startBtn) {
              startBtn.disabled = false;
              startBtn.innerText = 'Start Campaign Dispatch';
            }
            if (broadcastPollTimer) {
              clearInterval(broadcastPollTimer);
              broadcastPollTimer = null;
            }
            if (d.status === 'completed') {
              showToast('Campaign completed: ' + d.success + ' sent, ' + d.failed + ' failed', 'success');
            } else if (d.status === 'cancelled') {
              showToast('Campaign was cancelled', 'warn');
            }
          }
        }
      } catch (e) {
        console.error(e);
      }
    }

    async function cancelBroadcast() {
      showConfirm('Cancel Broadcast', 'Are you sure you want to stop the broadcast campaign? Remaining queued messages will not be sent.', async () => {
        try {
          const res = await apiFetch('/api/v1/campaigns/cancel', { method: 'POST' });
          const d = await res.json();
          if (res.ok) {
            showToast('Broadcast cancellation requested', 'info');
            pollBroadcastProgress();
          } else {
            showToast(d.error || 'Failed to cancel broadcast', 'error');
          }
        } catch (e) {
          showToast('Error cancelling broadcast: ' + e.message, 'error');
        }
      });
    }

    // Webhook Connectivity Test
    async function testWebhookPing() {
      const url = document.getElementById('whURL').value.trim();
      const secret = document.getElementById('whSecret').value.trim();
      if (!url) {
        showToast('Please enter an outbound webhook destination URL first', 'error');
        return;
      }
      showToast('Testing webhook connectivity...', 'info');
      try {
        const res = await apiFetch('/api/v1/webhook/test-ping', {
          method: 'POST',
          headers: {'Content-Type': 'application/json'},
          body: JSON.stringify({ url: url, secret: secret })
        });
        const d = await res.json();
        if (d.success) {
          showToast('Webhook reached. Status: ' + d.status_code + ' in ' + d.duration, 'success');
        } else {
          showToast('Ping failed (' + d.duration + '): ' + (d.error || ('Status ' + d.status_code)), 'error');
        }
      } catch (err) {
        showToast('Webhook ping failed: ' + err.message, 'error');
      }
    }

    // Audit Log Loader (S11)
    async function loadAuditLog() {
      try {
        const res = await apiFetch('/api/v1/audit-log?limit=50');
        if (!res.ok) return;
        const events = await res.json();
        const tbody = document.getElementById('auditTableBody');
        if (!events || events.length === 0) {
          tbody.innerHTML = '<tr><td colspan="5" style="text-align: center; color: var(--text-muted); padding: 24px;">No audit logs recorded yet.</td></tr>';
          return;
        }

        tbody.innerHTML = events.map(e => {
          const d = new Date(e.timestamp);
          const timeStr = d.toLocaleString();
          const badgeCls = e.status === 'success' ? 'badge-out' : 'badge-danger';
          return '<tr>' +
            '<td style="font-size: 11px; color: var(--text-muted);">' + timeStr + '</td>' +
            '<td><code style="color: #0284c7; font-weight: 600;">' + escapeHtml(e.action) + '</code></td>' +
            '<td>' + escapeHtml(e.user) + ' (' + escapeHtml(e.ip) + ')</td>' +
            '<td style="max-width: 300px; word-break: break-word;">' + escapeHtml(e.details) + '</td>' +
            '<td><span class="badge ' + badgeCls + '">' + escapeHtml(e.status) + '</span></td>' +
          '</tr>';
        }).join('');
        filterAuditLive();
      } catch (err) {
        console.error(err);
      }
    }

    // Privacy & Data Purge (C12)
    function purgeHistory(days) {
      showConfirm('Data Retention Purge', 'Are you sure you want to purge messages older than ' + days + ' days?', async () => {
        try {
          const res = await apiFetch('/api/v1/privacy/purge?days=' + days, { method: 'POST' });
          const d = await res.json();
          if (res.ok) {
            showToast(d.message || 'Purge completed', 'success');
            loadInbox();
          } else {
            showToast(d.error || 'Purge failed', 'error');
          }
        } catch (err) {
          showToast('Error executing purge', 'error');
        }
      });
    }

    function exportPrivacyBundle() {
      window.location.href = '/api/v1/privacy/export';
    }

    function exportContactsCSV() {
      window.location.href = '/api/v1/contacts/export-csv';
    }

    let qrPollTimer = null;

    // Regenerate QR Code immediately with active status polling
    async function regenerateQRCode() {
      const btn = document.getElementById('btnRegenQR');
      const statusMsg = document.getElementById('qrStatusMsg');
      const qrImg = document.getElementById('qrImage');

      if (btn) {
        btn.disabled = true;
        btn.innerHTML = '<svg class="icon spinner" viewBox="0 0 24 24"><path d="M21 12a9 9 0 1 1-6.219-8.56"></path></svg> <span>Generating QR...</span>';
      }
      if (statusMsg) {
        statusMsg.innerText = 'Resetting session and requesting fresh QR...';
        statusMsg.style.color = '#38bdf8';
      }

      // Show animated loading spinner immediately
      if (qrImg) {
        qrImg.src = '/api/v1/qr?t=' + Date.now();
      }

      try {
        const res = await apiFetch('/api/v1/relink', { method: 'POST' });
        const d = await res.json();
        if (!res.ok) {
          throw new Error(d.error || 'Failed to reset session');
        }
        showToast('Session reset. Previous chats & contacts cleared.', 'info');
        loadInbox();
        loadContacts();

        // Poll every 800ms for up to 15 seconds until fresh QR or Connected is detected
        if (qrPollTimer) clearInterval(qrPollTimer);
        let attempts = 0;
        qrPollTimer = setInterval(async () => {
          attempts++;
          try {
            const sRes = await apiFetch('/api/v1/status');
            if (sRes.ok) {
              const st = await sRes.json();
              const ws = st.whatsapp_client || {};
              const isConn = !!(st.whatsapp_connected || (ws && (ws.connected || ws.logged_in)));
              const hasQR = !!(ws && ws.latest_qr && ws.latest_qr.length > 0);

              if (qrImg) {
                qrImg.src = '/api/v1/qr?t=' + Date.now();
              }

              if (isConn) {
                clearInterval(qrPollTimer);
                qrPollTimer = null;
                if (btn) {
                  btn.disabled = false;
                  btn.innerHTML = '<svg class="icon" viewBox="0 0 24 24"><polyline points="23 4 23 10 17 10"></polyline><polyline points="1 20 1 14 7 14"></polyline><path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15"></path></svg> <span>Regenerate QR</span>';
                }
                if (statusMsg) {
                  statusMsg.innerText = 'WhatsApp Connected Successfully!';
                  statusMsg.style.color = '#10b981';
                }
                showToast('WhatsApp connected successfully!', 'success');
                checkStatusLive();
                return;
              }

              if (hasQR) {
                clearInterval(qrPollTimer);
                qrPollTimer = null;
                if (btn) {
                  btn.disabled = false;
                  btn.innerHTML = '<svg class="icon" viewBox="0 0 24 24"><polyline points="23 4 23 10 17 10"></polyline><polyline points="1 20 1 14 7 14"></polyline><path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15"></path></svg> <span>Regenerate QR</span>';
                }
                if (statusMsg) {
                  statusMsg.innerText = 'Fresh QR code ready. Please scan within 60s.';
                  statusMsg.style.color = '#10b981';
                }
                return;
              }
            }
          } catch (e) {}

          if (attempts >= 20) {
            clearInterval(qrPollTimer);
            qrPollTimer = null;
            if (btn) {
              btn.disabled = false;
              btn.innerHTML = '<svg class="icon" viewBox="0 0 24 24"><polyline points="23 4 23 10 17 10"></polyline><polyline points="1 20 1 14 7 14"></polyline><path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15"></path></svg> <span>Regenerate QR</span>';
            }
            if (statusMsg) {
              statusMsg.innerText = 'QR code ready. Scan with WhatsApp.';
              statusMsg.style.color = '#94a3b8';
            }
          }
        }, 800);

      } catch (err) {
        showToast(err.message || 'Error regenerating QR code', 'error');
        if (btn) {
          btn.disabled = false;
          btn.innerHTML = '<svg class="icon" viewBox="0 0 24 24"><polyline points="23 4 23 10 17 10"></polyline><polyline points="1 20 1 14 7 14"></polyline><path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15"></path></svg> <span>Regenerate QR</span>';
        }
        if (statusMsg) {
          statusMsg.innerText = 'Scan with WhatsApp > Linked Devices';
          statusMsg.style.color = '#94a3b8';
        }
      }
    }

    // Relink device
    function relinkDevice() {
      const statusText = document.getElementById('statusText');
      const isConnected = statusText && statusText.innerText.toLowerCase().includes('connected');
      if (isConnected) {
        showConfirm('Disconnect WhatsApp Device', 'Are you sure you want to disconnect? This will actively unlink this device from your phone\'s Linked Devices list and clear session contacts and chat history.', () => {
          regenerateQRCode();
        });
      } else {
        regenerateQRCode();
      }
    }

    let pairCodePollTimer = null;

    async function requestPairCode() {
      const phoneInput = document.getElementById('pairPhoneInput');
      const btn = document.getElementById('btnGetPairCode');
      const box = document.getElementById('pairCodeBox');
      const val = document.getElementById('pairCodeVal');
      const statusText = document.getElementById('pairCodeStatus');
      const phone = phoneInput ? phoneInput.value.trim() : '';

      if (!phone) {
        showToast('Please enter your phone number with country code (e.g. +919876543210)', 'error');
        return;
      }

      if (btn) {
        btn.disabled = true;
        btn.innerText = 'Connecting...';
      }
      if (statusText) {
        statusText.innerText = 'Requesting 8-digit link code from WhatsApp...';
        statusText.style.color = '#38bdf8';
      }

      try {
        const res = await apiFetch('/api/v1/pair-phone', {
          method: 'POST',
          headers: {'Content-Type': 'application/json'},
          body: JSON.stringify({ phone: phone })
        });
        const d = await res.json();
        if (res.ok && d.pair_code) {
          if (box) box.style.display = 'block';
          if (val) val.innerText = d.pair_code;
          if (statusText) {
            statusText.innerText = 'Code generated! Open WhatsApp on phone > Linked Devices > Link with phone number > Enter ' + d.pair_code;
            statusText.style.color = '#34d399';
          }
          showToast('Pairing code generated! Enter it on your phone now.', 'success');

          // Active polling every 1.2s waiting for user to enter code on phone
          if (pairCodePollTimer) clearInterval(pairCodePollTimer);
          let attempts = 0;
          pairCodePollTimer = setInterval(async () => {
            attempts++;
            try {
              const sRes = await apiFetch('/api/v1/status');
              if (sRes.ok) {
                const st = await sRes.json();
                const ws = st.whatsapp_client || {};
                const connected = !!(st.whatsapp_connected || (ws && (ws.connected || ws.logged_in)));

                if (connected) {
                  clearInterval(pairCodePollTimer);
                  pairCodePollTimer = null;
                  if (btn) {
                    btn.disabled = false;
                    btn.innerText = 'Get Code';
                  }
                  if (statusText) {
                    statusText.innerText = 'Phone linked successfully!';
                    statusText.style.color = '#10b981';
                  }
                  showToast('WhatsApp connected successfully via Phone Pairing!', 'success');
                  checkStatusLive();
                  return;
                }
              }
            } catch (e) {}

            if (attempts >= 60) {
              clearInterval(pairCodePollTimer);
              pairCodePollTimer = null;
              if (btn) {
                btn.disabled = false;
                btn.innerText = 'Get Code';
              }
              if (statusText) {
                statusText.innerText = 'Code timed out. Click "Get Code" to generate a new code.';
                statusText.style.color = '#f59e0b';
              }
            }
          }, 1200);

        } else {
          showToast(d.error || 'Failed to generate code', 'error');
          if (btn) {
            btn.disabled = false;
            btn.innerText = 'Get Code';
          }
        }
      } catch (err) {
        showToast('Pairing request error: ' + (err.message || err), 'error');
        if (btn) {
          btn.disabled = false;
          btn.innerText = 'Get Code';
        }
      }
    }

    // Rule Simulator
    async function simulateRule() {
      const text = document.getElementById('sandboxInput').value.trim();
      if (!text) return;
      try {
        const res = await apiFetch('/api/v1/test-rule', {
          method: 'POST',
          headers: {'Content-Type': 'application/json'},
          body: JSON.stringify({ message: text })
        });
        const d = await res.json();
        const box = document.getElementById('sandboxResult');
        box.style.display = 'block';
        if (d.matched) {
          document.getElementById('sandboxReplyText').innerText = 'Matched Rule [' + d.rule_name + ']:\n' + d.reply;
        } else {
          document.getElementById('sandboxReplyText').innerText = 'No rule matched for this input.';
        }
      } catch (e) {
        showToast('Simulator error', 'error');
      }
    }

    // Toggle Password Visibility
    function togglePasswordVisibility(id) {
      const el = document.getElementById(id);
      if (el) {
        el.type = el.type === 'password' ? 'text' : 'password';
      }
    }

    // Google Gemini AI Test Connection
    async function testGeminiApiKey() {
      const key = document.getElementById('aiKey').value.trim();
      const model = document.getElementById('aiModel').value.trim();
      const resSpan = document.getElementById('aiTestResult');
      const btn = document.getElementById('btnTestAI');

      if (!key) {
        showToast('Please enter a Gemini API Key first', 'error');
        return;
      }

      btn.disabled = true;
      btn.innerText = 'Testing...';
      if (resSpan) {
        resSpan.style.display = 'inline-block';
        resSpan.style.color = '#38bdf8';
        resSpan.innerText = 'Testing Gemini API connection...';
      }

      try {
        const res = await apiFetch('/api/v1/ai/test', {
          method: 'POST',
          headers: {'Content-Type': 'application/json'},
          body: JSON.stringify({ api_key: key, model: model })
        });
        const d = await res.json();
        if (res.ok && d.success) {
          if (resSpan) {
            resSpan.style.color = '#10b981';
            resSpan.innerText = 'Connected! Reply: "' + (d.reply || 'OK') + '"';
          }
          showToast('Gemini API key is valid and connected!', 'success');
        } else {
          if (resSpan) {
            resSpan.style.color = '#f87171';
            resSpan.innerText = 'Failed: ' + (d.error || 'Connection error');
          }
          showToast('Gemini test failed: ' + (d.error || 'Unknown error'), 'error');
        }
      } catch (err) {
        if (resSpan) {
          resSpan.style.color = '#f87171';
          resSpan.innerText = 'Network error';
        }
        showToast('Error connecting to Gemini API', 'error');
      } finally {
        btn.disabled = false;
        btn.innerHTML = '<svg class="icon" viewBox="0 0 24 24"><polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2"></polygon></svg> Test API Key';
      }
    }

    // Save AI Settings
    document.addEventListener('DOMContentLoaded', () => {
      const aiForm = document.getElementById('aiConfigForm');
      if (aiForm) {
        aiForm.addEventListener('submit', async (e) => {
          e.preventDefault();
          const btn = document.getElementById('btnSaveAI');
          if (btn) { btn.disabled = true; btn.innerText = 'Saving...'; }

          const enabled = document.getElementById('aiEnabled') ? document.getElementById('aiEnabled').checked : false;
          const key = document.getElementById('aiKey').value.trim();
          const model = document.getElementById('aiModel').value.trim();
          const prompt = document.getElementById('aiPrompt').value.trim();

          try {
            const res = await apiFetch('/api/v1/settings', {
              method: 'POST',
              headers: {'Content-Type': 'application/json'},
              body: JSON.stringify({
                ai: {
                  enabled: enabled,
                  api_key: key,
                  model: model,
                  system_prompt: prompt
                }
              })
            });
            const d = await res.json();
            if (res.ok) {
              showToast('Gemini AI settings saved successfully!', 'success');
            } else {
              showToast(d.error || 'Failed to save AI settings', 'error');
            }
          } catch (err) {
            showToast('Error saving AI settings', 'error');
          } finally {
            if (btn) { btn.disabled = false; btn.innerText = 'Save AI Settings'; }
          }
        });
      }

      // Save Business Hours
      const bhForm = document.getElementById('businessHoursForm');
      if (bhForm) {
        bhForm.addEventListener('submit', async (e) => {
          e.preventDefault();
          try {
            const res = await apiFetch('/api/v1/settings', {
              method: 'POST',
              headers: {'Content-Type': 'application/json'},
              body: JSON.stringify({
                business_hours: {
                  enabled: true,
                  timezone: document.getElementById('bhTimezone').value.trim(),
                  start_hour: parseInt(document.getElementById('bhStart').value, 10) || 9,
                  end_hour: parseInt(document.getElementById('bhEnd').value, 10) || 18,
                  away_reply: document.getElementById('bhAway').value.trim()
                }
              })
            });
            const d = await res.json();
            if (res.ok) {
              showToast('Business hours saved successfully!', 'success');
            } else {
              showToast(d.error || 'Failed to save business hours', 'error');
            }
          } catch (err) {
            showToast('Error saving business hours', 'error');
          }
        });
      }

      // CSV Bulk Import (C19)
      const csvForm = document.getElementById('csvImportForm');
      if (csvForm) {
        csvForm.addEventListener('submit', async (e) => {
          e.preventDefault();
          const textarea = document.getElementById('csvData');
          const data = textarea ? textarea.value.trim() : '';
          if (!data) {
            showToast('Please enter or paste CSV contact records', 'error');
            return;
          }
          const submitBtn = csvForm.querySelector('button[type="submit"]');
          if (submitBtn) { submitBtn.disabled = true; submitBtn.innerText = 'Importing...'; }

          try {
            const res = await apiFetch('/api/v1/contacts/import-csv', {
              method: 'POST',
              body: data
            });
            const d = await res.json();
            const feedbackEl = document.getElementById('csvImportFeedback');
            if (res.ok && (d.success || d.count !== undefined)) {
              let msg = 'CSV Imported: ' + (d.added || 0) + ' added, ' + (d.updated || 0) + ' updated';
              if (d.skipped > 0) msg += ', ' + d.skipped + ' skipped';
              showToast(msg, 'success');
              if (textarea) textarea.value = '';
              loadContacts();

              if (feedbackEl) {
                let html = '<div><strong>Import Summary:</strong> ' + (d.added || 0) + ' added, ' + (d.updated || 0) + ' updated, ' + (d.skipped || 0) + ' skipped.</div>';
                if (d.overwrites && d.overwrites.length > 0) {
                  html += '<div style="margin-top:6px; color:#2563eb;"><strong>Updated / Overwritten Contacts:</strong><ul style="margin:4px 0 0 16px; padding:0;">' +
                    d.overwrites.map(o => '<li>' + escapeHTML(o) + '</li>').join('') + '</ul></div>';
                }
                if (d.warnings && d.warnings.length > 0) {
                  html += '<div style="margin-top:6px; color:#dc2626;"><strong>Skipped Rows & Warnings:</strong><ul style="margin:4px 0 0 16px; padding:0;">' +
                    d.warnings.map(w => '<li>' + escapeHTML(w) + '</li>').join('') + '</ul></div>';
                }
                feedbackEl.innerHTML = html;
                feedbackEl.style.display = 'block';
              }
            } else {
              showToast(d.error || 'Failed to import CSV', 'error');
              if (feedbackEl) {
                feedbackEl.innerHTML = '<span style="color:#dc2626;">' + escapeHTML(d.error || 'Failed to import CSV') + '</span>';
                feedbackEl.style.display = 'block';
              }
            }
          } catch (err) {
            showToast('Error importing CSV: ' + err.message, 'error');
          } finally {
            if (submitBtn) { submitBtn.disabled = false; submitBtn.innerText = 'Import Contacts'; }
          }
        });
      }

      // Change Master Password (S2, S20)
      const cpForm = document.getElementById('changePasswordForm');
      if (cpForm) {
        cpForm.addEventListener('submit', async (e) => {
          e.preventDefault();
          const curr = document.getElementById('cpCurrent').value;
          const next = document.getElementById('cpNew').value;
          if (next.length < 8) {
            showToast('New password must be at least 8 characters long', 'error');
            return;
          }
          if (!/[a-zA-Z]/.test(next) || !/[0-9]/.test(next)) {
            showToast('New password must contain both letters and numbers', 'error');
            return;
          }
          const btn = cpForm.querySelector('button[type="submit"]');
          if (btn) { btn.disabled = true; btn.innerText = 'Updating...'; }

          try {
            const res = await apiFetch('/api/v1/settings/change-password', {
              method: 'POST',
              headers: {'Content-Type': 'application/json'},
              body: JSON.stringify({ current_password: curr, new_password: next })
            });
            const d = await res.json();
            if (res.ok && d.success) {
              showToast('Admin password updated successfully!', 'success');
              document.getElementById('cpCurrent').value = '';
              document.getElementById('cpNew').value = '';
            } else {
              showToast(d.error || 'Failed to update password', 'error');
            }
          } catch (err) {
            showToast('Error updating password: ' + err.message, 'error');
          } finally {
            if (btn) { btn.disabled = false; btn.innerText = 'Update Password'; }
          }
        });
      }

      // Broadcast Campaign Submit
      const bcForm = document.getElementById('broadcastForm');
      if (bcForm) {
        bcForm.addEventListener('submit', async (e) => {
          e.preventDefault();
          const audience = document.getElementById('bcAudience').value;
          const msg = document.getElementById('bcMessage').value.trim();
          const tmplId = document.getElementById('bcTemplate').value;

          if (!msg && !tmplId) {
            showToast('Please enter a message or choose a template', 'error');
            return;
          }

          let useAll = false;
          let tag = '';
          if (audience === 'all') {
            useAll = true;
          } else if (audience.startsWith('tag:')) {
            tag = audience.slice(4);
          }

          const btn = document.getElementById('startBroadcastBtn');
          if (btn) { btn.disabled = true; btn.innerText = 'Initiating...'; }

          try {
            const res = await apiFetch('/api/v1/campaigns/broadcast', {
              method: 'POST',
              headers: {'Content-Type': 'application/json'},
              body: JSON.stringify({
                use_all_contacts: useAll,
                tag: tag,
                message: msg,
                template_id: tmplId
              })
            });
            const d = await res.json();
            if (res.ok && d.success) {
              showToast('Broadcast started for ' + d.total_recipients + ' recipient(s)!', 'success');
              const box = document.getElementById('broadcastProgressBox');
              if (box) box.style.display = 'block';
              if (broadcastPollTimer) clearInterval(broadcastPollTimer);
              broadcastPollTimer = setInterval(pollBroadcastProgress, 1000);
              pollBroadcastProgress();
            } else {
              showToast(d.error || 'Failed to start broadcast', 'error');
              if (btn) { btn.disabled = false; btn.innerText = 'Start Campaign Dispatch'; }
            }
          } catch (err) {
            showToast('Error: ' + err.message, 'error');
            if (btn) { btn.disabled = false; btn.innerText = 'Start Campaign Dispatch'; }
          }
        });
      }

      // Outbound Webhook Settings Submit
      const whForm = document.getElementById('webhookConfigForm');
      if (whForm) {
        whForm.addEventListener('submit', async (e) => {
          e.preventDefault();
          const url = document.getElementById('whURL').value.trim();
          const secret = document.getElementById('whSecret').value.trim();
          const btn = whForm.querySelector('button[type="submit"]');
          if (btn) { btn.disabled = true; btn.innerText = 'Saving...'; }

          try {
            const res = await apiFetch('/api/v1/settings', {
              method: 'POST',
              headers: {'Content-Type': 'application/json'},
              body: JSON.stringify({
                webhook: {
                  url: url,
                  secret: secret,
                  events: ["message.received", "message.sent"]
                }
              })
            });
            const d = await res.json();
            if (res.ok) {
              showToast('Webhook settings saved successfully!', 'success');
            } else {
              showToast(d.error || 'Failed to save webhook settings', 'error');
            }
          } catch (err) {
            showToast('Error saving webhook settings: ' + err.message, 'error');
          } finally {
            if (btn) { btn.disabled = false; btn.innerText = 'Save Settings'; }
          }
        });
      }

      // Schedule Form Submit
      const schForm = document.getElementById('scheduleForm');
      if (schForm) {
        schForm.addEventListener('submit', async (e) => {
          e.preventDefault();
          const recipient = document.getElementById('schRecipient').value.trim();
          const name = document.getElementById('schName').value.trim();
          const dt = document.getElementById('schDateTime').value.trim();
          const msg = document.getElementById('schMessage').value.trim();
          const btn = document.getElementById('btnSaveSchedule');
          if (btn) { btn.disabled = true; btn.innerText = 'Scheduling...'; }

          try {
            const res = await apiFetch('/api/v1/scheduled', {
              method: 'POST',
              headers: {'Content-Type': 'application/json'},
              body: JSON.stringify({
                recipient: recipient,
                name: name,
                scheduled_at: dt,
                message: msg
              })
            });
            const d = await res.json();
            if (res.ok && (d.id || d.success)) {
              showToast('Message scheduled successfully!', 'success');
              closeScheduleModal();
              location.reload();
            } else {
              showToast(d.error || 'Failed to schedule message', 'error');
            }
          } catch (err) {
            showToast('Error scheduling message: ' + err.message, 'error');
          } finally {
            if (btn) { btn.disabled = false; btn.innerText = 'Schedule Message'; }
          }
        });
      }

      // Contact Form Submit
      const ctForm = document.getElementById('contactForm');
      if (ctForm) {
        ctForm.addEventListener('submit', async (e) => {
          e.preventDefault();
          const phone = document.getElementById('ctPhone').value.trim();
          const name = document.getElementById('ctName').value.trim();
          const email = document.getElementById('ctEmail').value.trim();
          const tagStr = document.getElementById('ctTags').value.trim();
          const notes = document.getElementById('ctNotes').value.trim();
          const tags = tagStr ? tagStr.split(',').map(s => s.trim()).filter(Boolean) : [];

          const btn = document.getElementById('btnSaveContact');
          if (btn) { btn.disabled = true; btn.innerText = 'Saving...'; }

          try {
            const res = await apiFetch('/api/v1/contacts', {
              method: 'POST',
              headers: {'Content-Type': 'application/json'},
              body: JSON.stringify({
                phone: phone,
                name: name,
                email: email,
                tags: tags,
                notes: notes
              })
            });
            const d = await res.json();
            if (res.ok && (d.phone || d.success)) {
              showToast('Contact saved successfully!', 'success');
              closeContactModal();
              loadContacts();
            } else {
              showToast(d.error || 'Failed to save contact', 'error');
            }
          } catch (err) {
            showToast('Error saving contact: ' + err.message, 'error');
          } finally {
            if (btn) { btn.disabled = false; btn.innerText = 'Save Contact'; }
          }
        });
      }

      // Template Form Submit
      const tmplForm = document.getElementById('templateForm');
      if (tmplForm) {
        tmplForm.addEventListener('submit', async (e) => {
          e.preventDefault();
          const name = document.getElementById('tmplName').value.trim();
          const category = document.getElementById('tmplCategory').value;
          const content = document.getElementById('tmplContent').value.trim();

          const btn = document.getElementById('btnSaveTemplate');
          if (btn) { btn.disabled = true; btn.innerText = 'Saving...'; }

          try {
            const res = await apiFetch('/api/v1/templates', {
              method: 'POST',
              headers: {'Content-Type': 'application/json'},
              body: JSON.stringify({
                id: name,
                name: name,
                category: category,
                content: content
              })
            });
            const d = await res.json();
            if (res.ok && (d.id || d.success)) {
              showToast('Template saved successfully!', 'success');
              closeTemplateModal();
              location.reload();
            } else {
              showToast(d.error || 'Failed to save template', 'error');
            }
          } catch (err) {
            showToast('Error saving template: ' + err.message, 'error');
          } finally {
            if (btn) { btn.disabled = false; btn.innerText = 'Save Template'; }
          }
        });
      }

      // Rule Form Submit
      const rForm = document.getElementById('ruleForm');
      if (rForm) {
        rForm.addEventListener('submit', async (e) => {
          e.preventDefault();
          const name = document.getElementById('ruleName').value.trim();
          const type = document.getElementById('ruleType').value;
          const pattern = document.getElementById('rulePattern').value.trim();
          const reply = document.getElementById('ruleReply').value.trim();
          const desc = document.getElementById('ruleDesc').value.trim();

          const btn = document.getElementById('btnSaveRule');
          if (btn) { btn.disabled = true; btn.innerText = 'Saving...'; }

          try {
            const res = await apiFetch('/api/v1/rules', {
              method: 'POST',
              headers: {'Content-Type': 'application/json'},
              body: JSON.stringify({
                name: name,
                type: type,
                pattern: pattern,
                reply: reply,
                description: desc
              })
            });
            const d = await res.json();
            if (res.ok && (d.name || d.success)) {
              showToast('Rule saved successfully!', 'success');
              closeRuleModal();
              location.reload();
            } else {
              showToast(d.error || 'Failed to save rule', 'error');
            }
          } catch (err) {
            showToast('Error saving rule: ' + err.message, 'error');
          } finally {
            if (btn) { btn.disabled = false; btn.innerText = 'Save Rule'; }
          }
        });
      }
    });


    let lastSeenQR = null;

    async function checkStatusLive() {
      try {
        const res = await apiFetch('/api/v1/status');
        if (!res.ok) return;
        const d = await res.json();
        const dot = document.getElementById('statusDot');
        const text = document.getElementById('statusText');
        const statusMsg = document.getElementById('qrStatusMsg');
        const qr = document.getElementById('qrImage');

        if (d) {
          const attnBadge = document.getElementById('navAttentionBadge');
          if (attnBadge) {
            if (d.attention_count && d.attention_count > 0) {
              attnBadge.innerText = d.attention_count;
              attnBadge.style.display = 'inline-block';
            } else {
              attnBadge.style.display = 'none';
            }
          }
        }

        let ws = (d && d.whatsapp_client) ? d.whatsapp_client : {};
        let isConn = !!(d.whatsapp_connected || (ws && (ws.connected || ws.logged_in)));
        let hasQR = !!(ws && ws.latest_qr && ws.latest_qr.length > 0);
        let qrCode = (ws && ws.latest_qr) ? ws.latest_qr : '';

        const str = isConn ? 'Connected' : (hasQR ? 'Waiting for QR' : 'Connecting...');
        if (text) text.innerText = str;
        if (dot) dot.className = 'status-dot ' + (isConn ? 'online' : 'connecting');

        // Toggle between Pairing Hero and Logged-In Account Hero
        const pairingHero = document.getElementById('pairingHero');
        const loggedInHero = document.getElementById('loggedInHero');
        if (pairingHero && loggedInHero) {
          if (isConn) {
            pairingHero.style.display = 'none';
            loggedInHero.style.display = 'block';

            const nameEl = document.getElementById('loginName');
            const phoneEl = document.getElementById('loginPhone');
            if (nameEl) nameEl.innerText = ws.push_name || ws.pushName || 'WhatsApp User';
            if (phoneEl) {
              const p = ws.phone || '';
              phoneEl.innerText = p ? (p.startsWith('+') ? p : '+' + p) : 'Active';
            }
          } else {
            pairingHero.style.display = 'block';
            loggedInHero.style.display = 'none';
          }
        }

        // Keep QR canvas in sync if on dashboard tab and not yet connected
        if (!isConn && document.getElementById('tab-dashboard')?.classList.contains('active')) {
          if (lastSeenQR !== qrCode) {
            lastSeenQR = qrCode;
            if (qr) qr.src = '/api/v1/qr?t=' + Date.now();
          }
          if (statusMsg) {
            if (hasQR) {
              statusMsg.innerText = 'Scan with WhatsApp > Linked Devices';
              statusMsg.style.color = '#94a3b8';
            } else {
              statusMsg.innerText = 'Connecting to WhatsApp servers...';
              statusMsg.style.color = '#38bdf8';
            }
          }
        }

        // Live synchronize sidebar contacts count with connection state
        const navContacts = document.getElementById('navContactsCount');
        if (navContacts) {
          navContacts.innerText = isConn ? (d.contacts_count || 0) : 0;
        }

        // When connection state changes (e.g. device logs out or connects), automatically reload contacts
        if (window.lastConnState !== undefined && window.lastConnState !== isConn) {
          window.lastConnState = isConn;
          loadContacts();
        } else if (window.lastConnState === undefined) {
          window.lastConnState = isConn;
        }
      } catch (e) {}
    }

    // Auto-refresh status, synchronize QR, and poll inbox when active
    setInterval(() => {
      checkStatusLive();
      const tabMsg = document.getElementById('tab-messages');
      if (tabMsg && tabMsg.classList.contains('active')) {
        loadInbox();
      }
    }, 4000);

    function openPasswordModal() {
      switchTab('settings');
      const el = document.getElementById('changePasswordForm');
      if (el) {
        el.scrollIntoView({ behavior: 'smooth' });
        const inp = document.getElementById('cpCurrent');
        if (inp) inp.focus();
      }
    }

    // Initial setup on load
    window.addEventListener('DOMContentLoaded', () => {
      const savedTab = localStorage.getItem('activePulseTab') || 'dashboard';
      switchTab(savedTab);
      loadContacts();
      checkStatusLive();
    });

    // ==========================================================
    // Global Event Delegation for Strict CSP Compliance (S12, X2)
    // ==========================================================
    function executeAction(action, target, event) {
      switch (action) {
        case 'openContactModal': openContactModal(); break;
        case 'closeContactModal': closeContactModal(); break;
        case 'openScheduleModal': openScheduleModal(); break;
        case 'closeScheduleModal': closeScheduleModal(); break;
        case 'openTemplateModal': openTemplateModal(); break;
        case 'closeTemplateModal': closeTemplateModal(); break;
        case 'openRuleModal': openRuleModal(); break;
        case 'closeRuleModal': closeRuleModal(); break;
        case 'openComplianceModal': openComplianceModal(); break;
        case 'closeComplianceModal': closeComplianceModal(); break;
        case 'closeConfirmModal': closeConfirmModal(); break;
        case 'openPasswordModal': openPasswordModal(); break;
        case 'userLogout': userLogout(); break;
        case 'toggleSidebar': toggleSidebar(); break;
        case 'refreshInbox': refreshInbox(true); break;
        case 'clearAllInbox': clearAllInbox(); break;
        case 'clearCurrentChat': clearCurrentChat(); break;
        case 'refreshCurrentChat': refreshCurrentChat(); break;
        case 'resumeBotForCurrentChat': resumeBotForCurrentChat(); break;
        case 'sendInboxMessage': sendInboxMessage(); break;
        case 'clearInboxFile': clearInboxFile(); break;
        case 'chooseInboxFile': {
          const inp = document.getElementById('inboxFile');
          if (inp) inp.click();
          break;
        }
        case 'exportContactsCSV': exportContactsCSV(); break;
        case 'exportPrivacyBundle': exportPrivacyBundle(); break;
        case 'testGeminiApiKey': testGeminiApiKey(); break;
        case 'testWebhookPing': testWebhookPing(); break;
        case 'regenerateQRCode': regenerateQRCode(); break;
        case 'relinkDevice': relinkDevice(); break;
        case 'requestPairCode': requestPairCode(); break;
        case 'simulateRule': simulateRule(); break;
        case 'loadAuditLog': loadAuditLog(); break;
        case 'cancelBroadcast': cancelBroadcast(); break;
        case 'togglePasswordVisibility': {
          const field = target.getAttribute('data-field') || 'aiKey';
          togglePasswordVisibility(field);
          break;
        }
        case 'purgeHistory': {
          const days = parseInt(target.getAttribute('data-days') || '30', 10);
          purgeHistory(days);
          break;
        }
        case 'insertQuickReply': {
          const reply = target.getAttribute('data-reply') || '';
          insertQuickReply(reply);
          break;
        }
        case 'copyToClipboard': {
          const text = target.getAttribute('data-copy-text') || '';
          const msg = target.getAttribute('data-success-msg') || 'Copied to clipboard!';
          copyToClipboard(text, msg);
          break;
        }
        case 'deleteContact': {
          const phone = target.getAttribute('data-phone');
          if (phone) deleteContact(phone);
          break;
        }
        case 'deleteRule': {
          const name = target.getAttribute('data-name');
          if (name) deleteRule(name);
          break;
        }
        case 'deleteScheduled': {
          const id = target.getAttribute('data-id');
          if (id) deleteScheduled(id);
          break;
        }
        case 'retryScheduled': {
          const id = target.getAttribute('data-id');
          if (id) retryScheduled(id);
          break;
        }
        case 'deleteTemplate': {
          const id = target.getAttribute('data-id');
          if (id) deleteTemplate(id);
          break;
        }
        case 'selectChat': {
          const phone = target.getAttribute('data-phone');
          const name = target.getAttribute('data-name');
          if (phone) {
            selectChat(phone, name);
            const switchTarget = target.getAttribute('data-switch-tab');
            if (switchTarget) switchTab(switchTarget);
          }
          break;
        }
      }
    }

    document.addEventListener('click', (e) => {
      // 1. Navigation tab clicks
      const tabLink = e.target.closest('[data-tab]');
      if (tabLink) {
        e.preventDefault();
        const tab = tabLink.getAttribute('data-tab');
        switchTab(tab);
        return;
      }

      // 2. Backdrop dismiss
      if (e.target.hasAttribute('data-close-backdrop')) {
        const modalId = e.target.getAttribute('data-close-backdrop');
        const modal = document.getElementById(modalId);
        if (modal) modal.classList.remove('open');
        return;
      }

      // 3. Named action buttons
      const actionEl = e.target.closest('[data-action]');
      if (actionEl) {
        const action = actionEl.getAttribute('data-action');
        executeAction(action, actionEl, e);
        return;
      }
    });

    // Keyboard accessibility (X2): Escape closes open modal, Enter sends chat message
    document.addEventListener('keydown', (e) => {
      if (e.key === 'Escape') {
        document.querySelectorAll('.modal-backdrop.open').forEach(m => m.classList.remove('open'));
      }
    });

    window.addEventListener('DOMContentLoaded', () => {
      const searchInp = document.getElementById('inboxSearchInput');
      if (searchInp) {
        searchInp.addEventListener('input', filterInboxContacts);
      }
      const chatInput = document.getElementById('chatInput');
      if (chatInput) {
        chatInput.addEventListener('keydown', (e) => {
          if (e.key === 'Enter' && !e.shiftKey) {
            e.preventDefault();
            sendInboxMessage();
          }
        });
      }
    });
`
