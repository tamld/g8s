/**
 * g8s Product Site — Interactive Controls
 * Zero dependencies · CSP-clean · Vanilla JS (ES2022)
 * Manages tab switching, copy-to-clipboard interactions, and keyboard accessibility
 */

(function () {
  'use strict';

  // Initialize all interactive modules once DOM is parsed
  function init() {
    setupTerminalTabs();
    setupQuickstartTabs();
    setupCopyButtons();
    setupKeyboardTabs();
  }

  /**
   * Hero Section Fig. 1 Terminal tab switcher
   */
  function setupTerminalTabs() {
    const tabList = document.querySelector('[data-terminal-tabs]');
    if (!tabList) return;

    const tabs = tabList.querySelectorAll('[role="tab"]');
    const panes = document.querySelectorAll('[data-terminal-pane]');

    tabs.forEach((tab) => {
      tab.addEventListener('click', () => {
        const targetId = tab.getAttribute('aria-controls');

        tabs.forEach((t) => {
          t.setAttribute('aria-selected', 'false');
          t.tabIndex = -1;
        });
        tab.setAttribute('aria-selected', 'true');
        tab.tabIndex = 0;

        panes.forEach((pane) => {
          if (pane.id === targetId) {
            pane.classList.add('active');
            pane.hidden = false;
          } else {
            pane.classList.remove('active');
            pane.hidden = true;
          }
        });
      });
    });
  }

  /**
   * Quickstart Section Multi-Platform switcher
   */
  function setupQuickstartTabs() {
    const tabList = document.querySelector('[data-qs-tabs]');
    if (!tabList) return;

    const tabs = tabList.querySelectorAll('[role="tab"]');
    const panels = document.querySelectorAll('[data-qs-panel]');

    tabs.forEach((tab) => {
      tab.addEventListener('click', () => {
        const targetId = tab.getAttribute('aria-controls');

        tabs.forEach((t) => {
          t.setAttribute('aria-selected', 'false');
          t.tabIndex = -1;
        });
        tab.setAttribute('aria-selected', 'true');
        tab.tabIndex = 0;

        panels.forEach((panel) => {
          if (panel.id === targetId) {
            panel.classList.add('active');
            panel.hidden = false;
          } else {
            panel.classList.remove('active');
            panel.hidden = true;
          }
        });
      });
    });
  }

  /**
   * Copy to clipboard buttons with visual feedback
   */
  function setupCopyButtons() {
    const copyButtons = document.querySelectorAll('.copy-btn');

    copyButtons.forEach((btn) => {
      btn.addEventListener('click', async () => {
        const targetSelector = btn.getAttribute('data-copy-target');
        let textToCopy = '';

        if (targetSelector) {
          const targetEl = document.querySelector(targetSelector);
          if (targetEl) {
            textToCopy = targetEl.innerText.trim();
          }
        } else if (btn.dataset.copyText) {
          textToCopy = btn.dataset.copyText.trim();
        }

        if (!textToCopy) return;

        try {
          if (navigator.clipboard && navigator.clipboard.writeText) {
            await navigator.clipboard.writeText(textToCopy);
          } else {
            // Fallback for non-secure or restricted contexts
            const textarea = document.createElement('textarea');
            textarea.value = textToCopy;
            textarea.style.position = 'fixed';
            textarea.style.opacity = '0';
            document.body.appendChild(textarea);
            textarea.select();
            document.execCommand('copy');
            document.body.removeChild(textarea);
          }

          const originalText = btn.innerHTML;
          btn.classList.add('copied');
          btn.innerHTML = '<span aria-hidden="true">✓</span> Copied';
          btn.setAttribute('aria-label', 'Copied to clipboard');

          setTimeout(() => {
            btn.classList.remove('copied');
            btn.innerHTML = originalText;
            btn.setAttribute('aria-label', 'Copy to clipboard');
          }, 2000);
        } catch (err) {
          console.error('Failed to copy to clipboard', err);
        }
      });
    });
  }

  /**
   * W3C ARIA TabList keyboard navigation (ArrowLeft / ArrowRight / Home / End)
   */
  function setupKeyboardTabs() {
    const tablists = document.querySelectorAll('[role="tablist"]');

    tablists.forEach((tablist) => {
      const tabs = Array.from(tablist.querySelectorAll('[role="tab"]'));

      tablist.addEventListener('keydown', (e) => {
        const currentTab = document.activeElement;
        const index = tabs.indexOf(currentTab);
        if (index === -1) return;

        let nextIndex = null;

        if (e.key === 'ArrowRight' || e.key === 'ArrowDown') {
          nextIndex = (index + 1) % tabs.length;
        } else if (e.key === 'ArrowLeft' || e.key === 'ArrowUp') {
          nextIndex = (index - 1 + tabs.length) % tabs.length;
        } else if (e.key === 'Home') {
          nextIndex = 0;
        } else if (e.key === 'End') {
          nextIndex = tabs.length - 1;
        }

        if (nextIndex !== null) {
          e.preventDefault();
          tabs[nextIndex].focus();
          tabs[nextIndex].click();
        }
      });
    });
  }

  // Run on DOM ready
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
