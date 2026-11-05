/**
 * g8s Hero Engine — Canvas2D isometric containment loop
 * submit → queue (SQLite WAL) → CAS lease → worktree → write receipt → verify gate
 * Zero dependencies · lazy IntersectionObserver · prefers-reduced-motion static frame
 */
(function () {
  'use strict';

  var DEG2RAD = Math.PI / 180;
  var COS30 = Math.cos(30 * DEG2RAD);
  var SIN30 = Math.sin(30 * DEG2RAD);

  /** @type {ReadonlyArray<{ id: string, label: string, sub: string }>} */
  var STAGES = [
    { id: 'submit', label: 'submit', sub: 'g8s submit' },
    { id: 'queue', label: 'queue', sub: 'SQLite WAL · single winner' },
    { id: 'lease', label: 'CAS lease', sub: 'compare-and-swap claim' },
    { id: 'worktree', label: 'worktree', sub: 'isolated git sandbox' },
    { id: 'receipt', label: 'write receipt', sub: 'path-scoped seal' },
    { id: 'verify', label: 'verify gate', sub: 'lanes 0 / D / R / F / S' }
  ];

  /** Captured values from index.html terminal panes — not fabricated outcomes */
  var CAPTURE = {
    taskPrefix: '015ea318',
    receiptPrefix: '67992e6a',
    leaseSeconds: 60
  };

  var LOOP_MS = 14000;
  var SEGMENT_MS = LOOP_MS / STAGES.length;

  function project3D(x, y, z, cam, vp) {
    var pitch = cam.pitch;
    var yaw = cam.yaw;
    var s = cam.scale;
    var cx = vp.cx;
    var cy = vp.cy;
    var y0 = y - cam.focusY;

    var cosY = Math.cos(yaw);
    var sinY = Math.sin(yaw);
    var x1 = x * cosY + z * sinY;
    var y1 = y0;
    var z1 = -x * sinY + z * cosY;

    var cosP = Math.cos(pitch);
    var sinP = Math.sin(pitch);
    var x2 = x1;
    var y2 = y1 * cosP - z1 * sinP;
    var z2 = y1 * sinP + z1 * cosP;

    var sx = cx + (x2 - z2) * COS30 * s;
    var sy = cy + (x2 + z2) * SIN30 * s - y2 * s;
    var depth = x2 + z2 + 2 * y2;

    return { x: sx, y: sy, depth: depth };
  }

  function easeB7(t) {
    if (t <= 0) return 0;
    if (t >= 1) return 1;
    var u = 1 - t;
    return 1 - u * u * u * u;
  }

  function readTheme(root) {
    var cs = getComputedStyle(root);
    return {
      ground: cs.getPropertyValue('--bg-ground').trim() || '#080c14',
      surface: cs.getPropertyValue('--bg-surface').trim() || '#0f1622',
      elevated: cs.getPropertyValue('--bg-surface-elevated').trim() || '#152030',
      border: cs.getPropertyValue('--border-subtle').trim() || '#1e2b3c',
      green: cs.getPropertyValue('--signal-green').trim() || '#05df72',
      amber: cs.getPropertyValue('--signal-amber').trim() || '#f59e0b',
      blue: cs.getPropertyValue('--signal-blue').trim() || '#38bdf8',
      text: cs.getPropertyValue('--text-secondary').trim() || '#94a3b8',
      textHi: cs.getPropertyValue('--text-primary').trim() || '#f8fafc'
    };
  }

  function stageCenters() {
    var out = [];
    var span = 72;
    var start = -((STAGES.length - 1) * span) / 2;
    for (var i = 0; i < STAGES.length; i++) {
      var x = start + i * span;
      out.push({ x: x, y: 0, z: -i * 8 });
    }
    return out;
  }

  function G8sHeroEngine(container) {
    this.container = container;
    this.canvas = container.querySelector('canvas');
    this.readout = container.querySelector('[data-hero-readout]');
    if (!this.canvas) return;

    this.ctx = this.canvas.getContext('2d');
    this.centers = stageCenters();
    this.theme = readTheme(container);
    this.camera = { pitch: 0.52, yaw: -0.38, focusY: 0, scale: 1 };
    this.viewport = { width: 0, height: 0, cx: 0, cy: 0, dpr: 1 };
    this.time = 0;
    this.rafId = null;
    this.running = false;
    this.lastTs = 0;
    this.reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)').matches;
    this.visible = false;

    var self = this;
    this._onResize = function () { self.resize(); };
    window.addEventListener('resize', this._onResize);

    var mql = window.matchMedia('(prefers-reduced-motion: reduce)');
    var onMotion = function (e) {
      self.reducedMotion = e.matches;
      if (self.reducedMotion) {
        self.stopLoop();
        self.drawStatic();
      } else if (self.visible) {
        self.startLoop();
      }
    };
    if (mql.addEventListener) mql.addEventListener('change', onMotion);
    else if (mql.addListener) mql.addListener(onMotion);

    var observer = new IntersectionObserver(function (entries) {
      entries.forEach(function (entry) {
        self.visible = entry.isIntersecting;
        if (!self.visible) {
          self.stopLoop();
          return;
        }
        if (self.reducedMotion) {
          self.drawStatic();
        } else {
          self.startLoop();
        }
      });
    }, { threshold: 0.08, rootMargin: '40px' });
    observer.observe(container);

    this.resize();
    if (this.reducedMotion) {
      this.drawStatic();
    }
  }

  G8sHeroEngine.prototype.resize = function () {
    var rect = this.container.getBoundingClientRect();
    var dpr = Math.min(window.devicePixelRatio || 1, 2);
    this.viewport.width = rect.width;
    this.viewport.height = rect.height;
    this.viewport.cx = rect.width / 2;
    this.viewport.cy = rect.height * 0.52;
    this.viewport.dpr = dpr;

    this.canvas.width = Math.max(1, Math.round(rect.width * dpr));
    this.canvas.height = Math.max(1, Math.round(rect.height * dpr));
    this.ctx.setTransform(dpr, 0, 0, dpr, 0, 0);

    if (rect.width < 400) {
      this.camera.scale = Math.max(0.62, rect.width / 420);
    } else if (rect.width < 720) {
      this.camera.scale = 0.82;
    } else {
      this.camera.scale = 0.95;
    }

    if (this.reducedMotion && !this.running) {
      this.drawStatic();
    }
  };

  G8sHeroEngine.prototype.startLoop = function () {
    if (this.running || this.reducedMotion) return;
    this.running = true;
    this.lastTs = performance.now();
    var self = this;
    function loop(ts) {
      if (!self.running) return;
      var dt = Math.min(48, ts - self.lastTs);
      self.lastTs = ts;
      self.time = (self.time + dt) % LOOP_MS;
      self.renderFrame();
      self.rafId = requestAnimationFrame(loop);
    }
    this.rafId = requestAnimationFrame(loop);
  };

  G8sHeroEngine.prototype.stopLoop = function () {
    this.running = false;
    if (this.rafId) {
      cancelAnimationFrame(this.rafId);
      this.rafId = null;
    }
  };

  G8sHeroEngine.prototype.stageIndex = function () {
    return Math.min(STAGES.length - 1, Math.floor(this.time / SEGMENT_MS));
  };

  G8sHeroEngine.prototype.segmentT = function () {
    return (this.time % SEGMENT_MS) / SEGMENT_MS;
  };

  G8sHeroEngine.prototype.packetPosition = function () {
    var idx = this.stageIndex();
    var t = easeB7(this.segmentT());
    var a = this.centers[idx];
    var b = this.centers[Math.min(idx + 1, STAGES.length - 1)];
    if (idx >= STAGES.length - 1) {
      return { x: b.x, y: b.y + 18, z: b.z + 6 };
    }
    return {
      x: a.x + (b.x - a.x) * t,
      y: a.y + (b.y - a.y) * t + Math.sin(t * Math.PI) * 10,
      z: a.z + (b.z - a.z) * t
    };
  };

  G8sHeroEngine.prototype.updateReadout = function (stageIdx, segT) {
    if (!this.readout) return;
    var stage = STAGES[stageIdx];
    var msg = stage.label + ' — ' + stage.sub;
    if (stage.id === 'lease') {
      var remain = Math.max(0, Math.ceil(CAPTURE.leaseSeconds * (1 - segT)));
      msg += ' · ' + remain + 's';
    }
    if (stage.id === 'submit') {
      msg += ' · task ' + CAPTURE.taskPrefix;
    }
    if (stage.id === 'receipt') {
      msg += ' · receipt ' + CAPTURE.receiptPrefix;
    }
    if (stage.id === 'verify') {
      var lanes = ['0', 'D', 'R', 'F', 'S'];
      var lane = lanes[Math.floor(segT * lanes.length) % lanes.length];
      msg += ' · lane ' + lane;
    }
    this.readout.textContent = msg;
  };

  G8sHeroEngine.prototype.drawGrid = function (ctx, cam, vp, theme) {
    ctx.save();
    ctx.strokeStyle = theme.border;
    ctx.globalAlpha = 0.35;
    ctx.lineWidth = 1;
    for (var i = -4; i <= 4; i++) {
      var p0 = project3D(-260, 0, i * 40, cam, vp);
      var p1 = project3D(260, 0, i * 40, cam, vp);
      ctx.beginPath();
      ctx.moveTo(p0.x, p0.y);
      ctx.lineTo(p1.x, p1.y);
      ctx.stroke();
      var q0 = project3D(i * 40, 0, -260, cam, vp);
      var q1 = project3D(i * 40, 0, 260, cam, vp);
      ctx.beginPath();
      ctx.moveTo(q0.x, q0.y);
      ctx.lineTo(q1.x, q1.y);
      ctx.stroke();
    }
    ctx.restore();
  };

  G8sHeroEngine.prototype.drawIsoPad = function (ctx, cx, cy, cz, w, d, h, cam, vp, theme, active, accent) {
    var hw = w / 2;
    var hd = d / 2;
    var top = [
      project3D(cx - hw, cy + h, cz - hd, cam, vp),
      project3D(cx + hw, cy + h, cz - hd, cam, vp),
      project3D(cx + hw, cy + h, cz + hd, cam, vp),
      project3D(cx - hw, cy + h, cz + hd, cam, vp)
    ];
    var left = [
      top[3],
      top[0],
      project3D(cx - hw, cy, cz - hd, cam, vp),
      project3D(cx - hw, cy, cz + hd, cam, vp)
    ];
    var right = [
      top[1],
      top[2],
      project3D(cx + hw, cy, cz + hd, cam, vp),
      project3D(cx + hw, cy, cz - hd, cam, vp)
    ];

    function poly(points, fill) {
      ctx.beginPath();
      ctx.moveTo(points[0].x, points[0].y);
      for (var i = 1; i < points.length; i++) ctx.lineTo(points[i].x, points[i].y);
      ctx.closePath();
      ctx.fillStyle = fill;
      ctx.fill();
    }

    var base = active ? theme.elevated : theme.surface;
    var edge = active ? accent : theme.border;
    poly(left, active ? 'rgba(15, 22, 34, 0.95)' : 'rgba(12, 18, 28, 0.92)');
    poly(right, 'rgba(8, 12, 20, 0.96)');
    poly(top, base);

    ctx.strokeStyle = edge;
    ctx.lineWidth = active ? 1.6 : 1;
    ctx.beginPath();
    for (var j = 0; j < top.length; j++) {
      if (j === 0) ctx.moveTo(top[j].x, top[j].y);
      else ctx.lineTo(top[j].x, top[j].y);
    }
    ctx.closePath();
    ctx.stroke();
  };

  G8sHeroEngine.prototype.drawLabel = function (ctx, x, y, z, title, sub, cam, vp, theme, active) {
    var p = project3D(x, y, z, cam, vp);
    ctx.save();
    ctx.textAlign = 'center';
    ctx.font = '600 11px ' + getComputedStyle(document.body).fontFamily;
    ctx.fillStyle = active ? theme.textHi : theme.text;
    ctx.fillText(title, p.x, p.y - 6);
    ctx.font = '10px monospace';
    ctx.fillStyle = active ? theme.green : theme.text;
    ctx.globalAlpha = active ? 1 : 0.85;
    ctx.fillText(sub, p.x, p.y + 8);
    ctx.restore();
  };

  G8sHeroEngine.prototype.drawPacket = function (ctx, pos, cam, vp, theme) {
    var s = 11 * cam.scale;
    var p = project3D(pos.x, pos.y, pos.z, cam, vp);
    ctx.save();
    ctx.translate(p.x, p.y);
    ctx.fillStyle = theme.amber;
    ctx.strokeStyle = theme.textHi;
    ctx.lineWidth = 1;
    ctx.beginPath();
    ctx.moveTo(0, -s);
    ctx.lineTo(s * 0.85, 0);
    ctx.lineTo(0, s);
    ctx.lineTo(-s * 0.85, 0);
    ctx.closePath();
    ctx.fill();
    ctx.stroke();
    ctx.restore();
  };

  G8sHeroEngine.prototype.drawGateLanes = function (ctx, cx, cy, cz, cam, vp, theme, activeLane) {
    var lanes = ['0', 'D', 'R', 'F', 'S'];
    var base = project3D(cx, cy + 26, cz, cam, vp);
    for (var i = 0; i < lanes.length; i++) {
      var ox = (i - 2) * 14;
      var p = project3D(cx + ox, cy + 28, cz + 4, cam, vp);
      var lit = lanes[i] === activeLane;
      ctx.fillStyle = lit ? theme.green : theme.surface;
      ctx.strokeStyle = lit ? theme.green : theme.border;
      ctx.lineWidth = 1;
      var rw = 12;
      var rh = 8;
      ctx.beginPath();
      ctx.rect(p.x - rw / 2, p.y - rh / 2, rw, rh);
      ctx.fill();
      ctx.stroke();
      ctx.font = '9px monospace';
      ctx.fillStyle = lit ? '#080c14' : theme.text;
      ctx.textAlign = 'center';
      ctx.textBaseline = 'middle';
      ctx.fillText(lanes[i], p.x, p.y + 0.5);
    }
    ctx.textAlign = 'center';
    ctx.font = '9px monospace';
    ctx.fillStyle = theme.text;
    ctx.fillText('gate lane', base.x, base.y + 18);
  };

  G8sHeroEngine.prototype.renderScene = function (stageIdx, segT, staticMode) {
    var ctx = this.ctx;
    var w = this.viewport.width;
    var h = this.viewport.height;
    var cam = this.camera;
    var vp = this.viewport;
    var theme = this.theme;

    ctx.clearRect(0, 0, w, h);
    ctx.fillStyle = theme.ground;
    ctx.fillRect(0, 0, w, h);

    this.drawGrid(ctx, cam, vp, theme);

    var drawList = [];
    var self = this;

    for (var i = 0; i < STAGES.length; i++) {
      (function (index) {
        var c = self.centers[index];
        var active = index === stageIdx;
        var accent = theme.green;
        if (STAGES[index].id === 'lease') accent = theme.amber;
        if (STAGES[index].id === 'receipt') accent = theme.blue;
        var depth = project3D(c.x, c.y, c.z, cam, vp).depth;
        drawList.push({
          depth: depth,
          draw: function () {
            self.drawIsoPad(ctx, c.x, c.y, c.z, 38, 28, active ? 14 : 10, cam, vp, theme, active, accent);
            self.drawLabel(ctx, c.x, c.y, c.z, STAGES[index].label, STAGES[index].sub, cam, vp, theme, active);
            if (STAGES[index].id === 'verify' && (active || staticMode)) {
              var lane = staticMode ? 'S' : ['0', 'D', 'R', 'F', 'S'][Math.floor(segT * 5) % 5];
              self.drawGateLanes(ctx, c.x, c.y, c.z, cam, vp, theme, lane);
            }
            if (STAGES[index].id === 'lease' && active && !staticMode) {
              var lp = project3D(c.x, c.y + 22, c.z, cam, vp);
              var remain = Math.max(0, Math.ceil(CAPTURE.leaseSeconds * (1 - segT)));
              ctx.font = '11px monospace';
              ctx.fillStyle = theme.amber;
              ctx.textAlign = 'center';
              ctx.fillText('CAS · ' + remain + 's', lp.x, lp.y);
            }
          }
        });
      })(i);
    }

    var packet = staticMode
      ? { x: self.centers[5].x, y: self.centers[5].y + 18, z: self.centers[5].z + 6 }
      : self.packetPosition();
    var pp = project3D(packet.x, packet.y, packet.z, cam, vp);
    drawList.push({
      depth: pp.depth + 50,
      draw: function () {
        self.drawPacket(ctx, packet, cam, vp, theme);
      }
    });

    drawList.sort(function (a, b) { return a.depth - b.depth; });
    for (var d = 0; d < drawList.length; d++) drawList[d].draw();

    this.updateReadout(stageIdx, segT);
  };

  G8sHeroEngine.prototype.renderFrame = function () {
    this.renderScene(this.stageIndex(), this.segmentT(), false);
  };

  G8sHeroEngine.prototype.drawStatic = function () {
    this.renderScene(STAGES.length - 1, 1, true);
    if (this.readout) {
      this.readout.textContent =
        'verify gate — lanes 0 / D / R / F / S (static frame · prefers-reduced-motion)';
    }
  };

  function boot() {
    var el = document.getElementById('g8s-hero-engine');
    if (el) new G8sHeroEngine(el);
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', boot);
  } else {
    boot();
  }
})();
