/**
 * Aegis Sovereign Governance Framework — Interactive Controls
 * Zero dependencies · CSP-clean · Vanilla JS (ES2022)
 * Meets B7 Motion Law & W3C ARIA Accessible Standards
 */

(function () {
  'use strict';

  // Initialize interactive components when DOM is ready
  function init() {
    setupMobileNav();
    setupSimulator();
    setupCognitiveLoopTabs();
    setupGovernanceLaneTabs();
    setupCopyButtons();
    setupKeyboardTabs();
    setupScrollReveals();
  }

  /**
   * Mobile Navigation Toggle
   */
  function setupMobileNav() {
    const toggleBtn = document.querySelector('[data-mobile-toggle]');
    const navMenu = document.querySelector('[data-nav-menu]');
    if (!toggleBtn || !navMenu) return;

    toggleBtn.addEventListener('click', () => {
      const isExpanded = toggleBtn.getAttribute('aria-expanded') === 'true';
      toggleBtn.setAttribute('aria-expanded', String(!isExpanded));
      navMenu.classList.toggle('nav-open', !isExpanded);
    });

    // Close menu when clicking outside or clicking any nav link
    const navLinks = navMenu.querySelectorAll('a');
    navLinks.forEach((link) => {
      link.addEventListener('click', () => {
        toggleBtn.setAttribute('aria-expanded', 'false');
        navMenu.classList.remove('nav-open');
      });
    });
  }

  /**
   * Interactive Covenant Ratchet & Anti-Drift Simulator
   */
  function setupSimulator() {
    const requirementInput = document.getElementById('sim-requirement-input');
    const presetSelect = document.getElementById('sim-preset-select');
    const compileBtn = document.getElementById('sim-compile-btn');
    const stepBtn = document.getElementById('sim-step-btn');
    const injectBtn = document.getElementById('sim-inject-btn');
    const resetBtn = document.getElementById('sim-reset-btn');

    const digestEl = document.getElementById('sim-digest-val');
    const batonEl = document.getElementById('sim-baton-val');
    const statusPill = document.getElementById('sim-status-pill');
    const logBox = document.getElementById('sim-log-box');
    const stageIndicator = document.getElementById('sim-stage-indicator');

    if (!compileBtn || !stepBtn || !injectBtn || !resetBtn) return;

    // Presets catalog
    const presets = {
      auth: {
        text: 'Implement JWT token authentication with rate limiting and revocation list in SQLite.',
        digest: 'sha256:7f83b1657ff1fc53b92dc18148a1d65dfc2d4b1fa3d677284addd200126d9069',
        paths: ['internal/auth/**', 'internal/ratelimit/**'],
        forbidden: ['FORBID_ASSERT_DELETION', 'FORBID_MOCK_AUTH_SHORTCUT', 'FORBID_UNSCOPED_MUTATION']
      },
      tenant: {
        text: 'Refactor tenant schema migration with isolated namespaces and zero-downtime rollback.',
        digest: 'sha256:4a38e88e10d2c67b93850119e075c326085a6a6f1d0637175468798c199580b8',
        paths: ['schema/migrations/**', 'internal/tenant/**'],
        forbidden: ['FORBID_DATA_LOSS_CASCADE', 'FORBID_SKIPPED_SUITE', 'FORBID_MUTATION_OUTSIDE_SCOPE']
      },
      worker: {
        text: 'Eliminate race condition in worker event queue using compare-and-swap state lease.',
        digest: 'sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855',
        paths: ['internal/queue/**', 'internal/worker/**'],
        forbidden: ['FORBID_ASSERT_PRUNING', 'FORBID_EMPTY_STUB', 'FORBID_UNLOCKED_MUTATION']
      }
    };

    const stages = [
      { name: 'Stage 1: Intent Intake & Covenant Lock', detail: 'Compiled natural language into immutable SHA-256 covenant contract.' },
      { name: 'Stage 2: Disjoint DAG Decomposition', detail: 'Decomposed task into isolated subtasks with single-use path-scoped receipts.' },
      { name: 'Stage 3: 5D Causal Selection & Adversarial Debate', detail: '5 personas stress-tested architecture and queried historical precedents.' },
      { name: 'Stage 4: Monotonic Ratchet Verification', detail: 'Physical diff evidence verified. All assertions executed. High-water mark secured.' },
      { name: 'Stage 5: Retrospective & Institutional Maturation', detail: 'Task verified. Structured reflection receipt saved to institutional memory.' }
    ];

    let currentStageIndex = 0;
    let isCompiled = false;
    let isTripped = false;

    // Preset selection change
    if (presetSelect && requirementInput) {
      presetSelect.addEventListener('change', () => {
        const val = presetSelect.value;
        if (presets[val]) {
          requirementInput.value = presets[val].text;
        }
      });
    }

    // Helper: append log
    function appendLog(level, message) {
      if (!logBox) return;
      const time = new Date().toISOString().substring(11, 19);
      const row = document.createElement('div');
      row.className = 'sim-log-row sim-log-' + level;
      row.textContent = `[${time}] ${message}`;
      logBox.appendChild(row);
      logBox.scrollTop = logBox.scrollHeight;
    }

    // Action 1: Compile Covenant
    compileBtn.addEventListener('click', () => {
      const text = requirementInput ? requirementInput.value.trim() : '';
      if (!text) {
        appendLog('warn', 'Enter or select a requirement before compiling.');
        return;
      }

      isCompiled = true;
      isTripped = false;
      currentStageIndex = 0;

      // Select matching preset or generate pseudo-hash
      const presetKey = presetSelect ? presetSelect.value : 'auth';
      const preset = presets[presetKey] || presets.auth;

      if (digestEl) digestEl.textContent = preset.digest;
      if (batonEl) {
        batonEl.textContent = `Baton #01 | Token Budget: 2,400 | Invariants: 4 | Allowed: ${preset.paths.join(', ')}`;
      }

      if (statusPill) {
        statusPill.className = 'sim-status-pill status-armed';
        statusPill.textContent = 'COVENANT ARMED';
      }

      if (stageIndicator) {
        stageIndicator.textContent = stages[0].name;
      }

      if (logBox) logBox.innerHTML = '';
      appendLog('info', 'Compiling natural language intent into deterministic covenant contract...');
      appendLog('success', `Covenant sealed: ${preset.digest}`);
      appendLog('info', `Active forbidden rules: ${preset.forbidden.join(', ')}`);
      appendLog('info', 'Stage 1 initialized: Execution baton armed against goal drift.');

      stepBtn.disabled = false;
      injectBtn.disabled = false;
    });

    // Action 2: Advance Execution Stage
    stepBtn.addEventListener('click', () => {
      if (!isCompiled || isTripped) return;

      if (currentStageIndex < stages.length - 1) {
        currentStageIndex++;
        const current = stages[currentStageIndex];
        if (stageIndicator) stageIndicator.textContent = current.name;
        appendLog('info', `Advancing to ${current.name}...`);
        appendLog('success', current.detail);

        if (currentStageIndex === stages.length - 1) {
          stepBtn.disabled = true;
          if (statusPill) {
            statusPill.className = 'sim-status-pill status-verified';
            statusPill.textContent = 'FULLY VERIFIED';
          }
          appendLog('success', 'Closed-loop maturation complete. Zero goal drift observed.');
        }
      }
    });

    // Action 3: Inject Regression / Assertion Pruning
    injectBtn.addEventListener('click', () => {
      if (!isCompiled || isTripped) return;

      isTripped = true;
      stepBtn.disabled = true;
      injectBtn.disabled = true;

      if (statusPill) {
        statusPill.className = 'sim-status-pill status-tripped';
        statusPill.textContent = 'BREAKER TRIPPED (<12ms)';
      }

      appendLog('warn', 'INJECTING ADVERSARIAL MUTATION: AI agent attempted to delete test assertions to bypass failing suite...');
      appendLog('error', 'CRUCIBLE INTERCEPT: E_ASSERTION_PRUNING_DETECTED in physical diff.');
      appendLog('error', 'Crucible Rule #2 Violated: Sensor ≠ Decider. Code author cannot prune test assertions.');
      appendLog('warn', 'MONOTONIC RATCHET ACTIVATED: High-water mark decreased. Rewinding state to Checkpoint 02...');
      appendLog('info', 'Git worktree discarded. Single-use write receipt revoked. Process terminated.');
      appendLog('success', 'GUARANTEE HELD: Zero unverified or pruned diffs reached main.');
    });

    // Action 4: Reset
    resetBtn.addEventListener('click', () => {
      isCompiled = false;
      isTripped = false;
      currentStageIndex = 0;

      if (digestEl) digestEl.textContent = 'sha256:none (uncompiled)';
      if (batonEl) batonEl.textContent = 'Awaiting covenant compilation...';
      if (statusPill) {
        statusPill.className = 'sim-status-pill status-idle';
        statusPill.textContent = 'IDLE';
      }
      if (stageIndicator) stageIndicator.textContent = 'Awaiting intent compilation';
      if (logBox) {
        logBox.innerHTML = '';
        appendLog('info', 'Simulator reset. Select a requirement preset and click "Compile Covenant".');
      }

      stepBtn.disabled = true;
      injectBtn.disabled = true;
    });
  }

  /**
   * 5-Stage Cognitive Loop Tab Switcher
   */
  function setupCognitiveLoopTabs() {
    const tabList = document.querySelector('[data-loop-tabs]');
    if (!tabList) return;

    const tabs = tabList.querySelectorAll('[role="tab"]');
    const panes = document.querySelectorAll('[data-loop-pane]');

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
   * Risk-Proportional Governance Matrix Lane Switcher
   */
  function setupGovernanceLaneTabs() {
    const tabList = document.querySelector('[data-lane-tabs]');
    if (!tabList) return;

    const tabs = tabList.querySelectorAll('[role="tab"]');
    const panes = document.querySelectorAll('[data-lane-pane]');

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
   * Copy to Clipboard with Accessible Feedback
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
          btn.innerHTML = '<span class="copied-indicator" aria-hidden="true"></span> Copied';
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

  /**
   * B7 Motion Law — Scroll Reveals using IntersectionObserver
   * Reveals each section once, base curve cubic-bezier(0.22, 1, 0.36, 1)
   */
  function setupScrollReveals() {
    // Respect prefers-reduced-motion
    if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) {
      document.querySelectorAll('.reveal').forEach((el) => {
        el.classList.add('revealed');
      });
      return;
    }

    if (!('IntersectionObserver' in window)) {
      document.querySelectorAll('.reveal').forEach((el) => {
        el.classList.add('revealed');
      });
      return;
    }

    const observer = new IntersectionObserver((entries, obs) => {
      entries.forEach((entry) => {
        if (entry.isIntersecting) {
          entry.target.classList.add('revealed');
          obs.unobserve(entry.target);
        }
      });
    }, {
      root: null,
      threshold: 0.1,
      rootMargin: '0px 0px -40px 0px'
    });

    document.querySelectorAll('.reveal').forEach((el) => {
      observer.observe(el);
    });
  }

  // Execute on DOM ready
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
