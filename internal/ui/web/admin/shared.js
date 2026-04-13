// shared.js — common utilities for all admin pages.

// API helper — auto-attaches JWT, handles 401 → logout, unwraps response envelope.
async function apiCall(method, path, body) {
  const token = localStorage.getItem('wa_token');
  const opts = {
    method,
    headers: { 'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json' }
  };
  if (body) opts.body = JSON.stringify(body);
  const r = await fetch('/v1' + path, opts);
  if (r.status === 401) { doLogout(); return null; }
  if (r.status === 204) return null;
  const d = await r.json();
  if (!d.success) throw new Error(d.error?.message || 'Request failed');
  return d.data;
}

// DELETE helper (doesn't parse body).
async function apiDelete(path) {
  const token = localStorage.getItem('wa_token');
  const r = await fetch('/v1' + path, {
    method: 'DELETE',
    headers: { 'Authorization': 'Bearer ' + token }
  });
  if (r.status === 401) { doLogout(); return; }
}

function doLogout() {
  localStorage.removeItem('wa_token');
  localStorage.removeItem('wa_email');
  window.location.href = '/admin/';
}

function requireAuth() {
  if (!localStorage.getItem('wa_token')) {
    window.location.href = '/admin/';
    return false;
  }
  return true;
}

function getUserEmail() {
  return localStorage.getItem('wa_email') || '';
}

// Returns the uppercased first letter of the stored email, or '?' — safe to use
// directly in x-text without complex method chains (Alpine CSP evaluator friendly).
function getEmailInitial() {
  const e = getUserEmail();
  return e ? e.charAt(0).toUpperCase() : '?';
}

// Format date helper — shows date + time.
function fmtDate(s) {
  if (!s) return '\u2014';
  const d = new Date(s);
  if (isNaN(d)) return '\u2014';
  return d.toLocaleString(undefined, {
    year: '2-digit', month: 'short', day: 'numeric',
    hour: '2-digit', minute: '2-digit',
  });
}

// Format date only (no time).
function fmtDateOnly(s) {
  if (!s) return '\u2014';
  const d = new Date(s);
  if (isNaN(d)) return '\u2014';
  return d.toLocaleDateString(undefined, { year: '2-digit', month: 'short', day: 'numeric' });
}

// ---------------------------------------------------------------------------
// License / trial banner — injected into every admin page on DOMContentLoaded
// ---------------------------------------------------------------------------
(async function initLicenseBanner() {
  const token = localStorage.getItem('wa_token');
  if (!token) return;

  let status;
  try {
    const r = await fetch('/v1/license/status', {
      headers: { 'Authorization': 'Bearer ' + token }
    });
    if (!r.ok) return;
    const d = await r.json();
    status = d.data || d; // support both envelope and raw
  } catch (_) { return; }

  if (!status || status.mode === 'licensed') return; // licensed — no banner

  const expired   = status.trial_expired;
  const daysLeft  = status.days_remaining ?? 0;
  const urgent    = !expired && daysLeft <= 3;

  let bg, border, text, icon, msg;

  if (expired) {
    bg = 'bg-red-950/70'; border = 'border-red-700/60'; text = 'text-red-300';
    icon = `<svg class="w-4 h-4 flex-shrink-0 mt-0.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2"><path stroke-linecap="round" stroke-linejoin="round" d="M12 9v2m0 4h.01M10.29 3.86L1.82 18a2 2 0 001.71 3h16.94a2 2 0 001.71-3L13.71 3.86a2 2 0 00-3.42 0z"/></svg>`;
    msg = '<strong>Trial expirado.</strong> Novas instâncias estão bloqueadas. Adicione uma <code class="bg-red-900/50 px-1 rounded text-xs">LICENSE_KEY</code> no seu <code class="bg-red-900/50 px-1 rounded text-xs">.env</code> para continuar.';
  } else if (urgent) {
    bg = 'bg-amber-950/70'; border = 'border-amber-700/60'; text = 'text-amber-300';
    icon = `<svg class="w-4 h-4 flex-shrink-0 mt-0.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2"><path stroke-linecap="round" stroke-linejoin="round" d="M12 9v2m0 4h.01M10.29 3.86L1.82 18a2 2 0 001.71 3h16.94a2 2 0 001.71-3L13.71 3.86a2 2 0 00-3.42 0z"/></svg>`;
    msg = `<strong>Trial termina em ${daysLeft} dia${daysLeft !== 1 ? 's' : ''}.</strong> Adicione uma <code class="bg-amber-900/50 px-1 rounded text-xs">LICENSE_KEY</code> para não perder o acesso.`;
  } else {
    bg = 'bg-blue-950/60'; border = 'border-blue-800/50'; text = 'text-blue-300';
    icon = `<svg class="w-4 h-4 flex-shrink-0 mt-0.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2"><path stroke-linecap="round" stroke-linejoin="round" d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z"/></svg>`;
    msg = `<strong>Trial ativo — ${daysLeft} dias restantes.</strong> Adicione uma <code class="bg-blue-900/50 px-1 rounded text-xs">LICENSE_KEY</code> no <code class="bg-blue-900/50 px-1 rounded text-xs">.env</code> para ativar seu plano.`;
  }

  const banner = document.createElement('div');
  banner.className = `flex items-start gap-2.5 px-4 py-2.5 text-xs border-b ${bg} ${border} ${text}`;
  banner.innerHTML = icon + `<span>${msg}</span>`;

  // Insert before the flex container (main layout wrapper)
  const body = document.body;
  const wrap = body.querySelector('.flex.h-screen');
  if (wrap) {
    body.insertBefore(banner, wrap);
    // Adjust outer wrapper height so layout doesn't break
    wrap.style.height = `calc(100vh - ${banner.offsetHeight}px)`;
  }
})();
