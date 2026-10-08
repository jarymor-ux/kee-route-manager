'use strict';

const activeDialogs = [];
function dialogFocusables(element) {
  return [...(element.querySelectorAll?.('button, input, select, textarea, a[href], [tabindex]') || [])]
    .filter((item) => !item.disabled && item.tabIndex !== -1 && !item.closest?.('.hidden') && item.getClientRects?.().length);
}
function syncOverlayInert() {
  const modal = activeDialogs.length > 0;
  const drawer = !modal && Boolean(document.body?.classList.contains('nav-open'));
  ['header', 'main', '#tabs'].forEach((selector) => {
    const element = $(selector);
    if (element) element.inert = modal || (drawer && selector !== '#tabs') || (selector === '#tabs' && !drawer && Boolean(window.matchMedia?.('(max-width: 760px)').matches));
  });
  document.body?.classList.toggle('dialog-open', modal);
}
function openDialog(id, focusSelector, dismissible = true) {
  const element = $(`#${id}`);
  const previous = activeDialogs.find((item) => item.id === id);
  if (!previous) activeDialogs.push({ id, focusSelector, dismissible, returnFocus: document.activeElement });
  element.classList.remove('hidden');
  element.setAttribute('role', 'dialog');
  element.setAttribute('aria-modal', 'true');
  syncOverlayInert();
  const focus = focusSelector ? $(focusSelector) : dialogFocusables(element)[0];
  focus?.focus();
}
function closeDialog(id) {
  const index = activeDialogs.findIndex((item) => item.id === id);
  const previous = index >= 0 ? activeDialogs.splice(index, 1)[0] : null;
  $(`#${id}`).classList.add('hidden');
  syncOverlayInert();
  if (!previous) return;
  if (previous.returnFocus?.isConnected && !previous.returnFocus.closest?.('.hidden') && !previous.returnFocus.inert) previous.returnFocus.focus();
  else if (authenticated) $('#logout')?.focus();
}
function activeOverlay() {
  const dialog = activeDialogs.at(-1);
  if (dialog) return { ...dialog, element: $(`#${dialog.id}`) };
  if (document.body?.classList.contains('nav-open')) return { id: 'tabs', dismissible: true, element: $('#tabs'), focusSelector: '#nav-close' };
  return null;
}
function dismissOverlay(overlay) {
  if (!overlay.dismissible) return;
  if (overlay.id === 'subscription-modal') clearSubscriptionEditor();
  else if (overlay.id === 'user-modal') clearUserEditor();
  else if (overlay.id === 'tabs') closeNavigation();
}
function initializeDialogs() {
  document.addEventListener?.('keydown', (event) => {
    const overlay = activeOverlay();
    if (!overlay) return;
    if (event.key === 'Escape') {
      event.preventDefault();
      dismissOverlay(overlay);
      return;
    }
    if (event.key !== 'Tab') return;
    const fields = dialogFocusables(overlay.element);
    if (!fields.length) { event.preventDefault(); return; }
    const index = fields.indexOf(document.activeElement);
    if (event.shiftKey && index <= 0) { event.preventDefault(); fields.at(-1).focus(); }
    else if (!event.shiftKey && (index === fields.length - 1 || index === -1)) { event.preventDefault(); fields[0].focus(); }
  });
  document.addEventListener?.('focusin', (event) => {
    const overlay = activeOverlay();
    if (!overlay || overlay.element.contains?.(event.target)) return;
    const focus = dialogFocusables(overlay.element)[0];
    focus?.focus();
  });
}
