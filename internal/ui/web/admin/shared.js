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

function isAdmin() {
  return localStorage.getItem('wa_role') === 'admin';
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

