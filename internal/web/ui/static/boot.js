'use strict';

// All deferred modules are ready before requesting an authenticated session.
initializeWorkspace();
initializeCharts();
initializeDialogs();
initializeSettingsEditor();
initializePanelEditor();

if ('serviceWorker' in navigator) navigator.serviceWorker.register('/sw.js').catch(() => {});
session();
