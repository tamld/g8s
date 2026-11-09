import React from 'react';
import { AbsoluteFill, useCurrentFrame, useVideoConfig, interpolate, spring } from 'remotion';

/* =====================================================================
   G8sReel — "One task, gate to gate." (36s @ 30fps, 1920×1080)
   Visual language: the operator console's own tokens (sites/home).
   Motion personality: professional-trust — bezier(0,0,0.2,1), no bounce.
   Storyboard (design spec §3, holds per shotcraft budget):
     S1   0- 96  cold open — command types with caret
     S2  96-192  QUEUED envelope
     S3 192-288  receipt card slides in
     S4 288-426  worker edits tick → E_DIFF_FIREWALL_OUT_OF_SCOPE impact
     S5 426-522  "The gate refuses. Loudly." hold (breathing)
     S6 522-618  fresh receipt, wider scope
     S7 618-768  gates PASS stack ×3
     S8 768-864  verify ok 6/6
     S9 864-972  commit + Receipt trailer
     S10 972-1080 end lockup
   ===================================================================== */

const C = {
  bg: '#080b0e', pane: '#0d1116', paneHi: '#12181e',
  ink: '#dbe6ee', dim: '#a9b6c1', faint: '#3a444e',
  gate: '#3fe389', amber: '#e5b855', err: '#f06a6a', recv: '#8cc9b1',
};
const EASE = 'cubic-bezier(0, 0, 0.2, 1)';
const MONO = 'ui-monospace, "SF Mono", "Cascadia Mono", Menlo, Consolas, monospace';

const abs = interpolate; // shorthand

/* ---- shared chrome: the console window ---- */
const Window: React.FC<{ title: string; children: React.ReactNode; scale: number; opacity: number }> =
({ title, children, scale, opacity }) => (
  <AbsoluteFill style={{ justifyContent: 'center', alignItems: 'center', opacity }}>
    <div style={{
      width: 1180, transform: `scale(${scale})`, background: C.pane,
      border: `1px solid ${C.faint}`, boxShadow: '0 30px 90px rgba(0,0,0,.55)',
    }}>
      <div style={{ display: 'flex', gap: 10, alignItems: 'center', padding: '13px 18px',
        borderBottom: `1px solid ${C.faint}`, background: C.paneHi, color: C.dim, fontSize: 16, letterSpacing: 1 }}>
        {[0, 1, 2].map(i => <i key={i} style={{ width: 12, height: 12, borderRadius: '50%', background: C.faint }} />)}
        <span style={{ marginLeft: 8 }}>{title}</span>
      </div>
      <div style={{ padding: '28px 34px', fontSize: 26, lineHeight: 1.75, minHeight: 520 }}>{children}</div>
    </div>
  </AbsoluteFill>
);

const CLS_COLOR: Record<string, string> = {
  '': C.ink, 't-ink': C.ink, 't-gate': C.gate, 't-err': C.err, 't-dim': C.dim,
  't-recv': C.recv, 't-warn': C.amber, 't-cyan': '#5db8c4',
};
const L: React.FC<{ cls: string; children: React.ReactNode; style?: React.CSSProperties }> =
({ cls, children, style }) => (
  <code style={{ display: 'block', whiteSpace: 'pre-wrap', wordBreak: 'break-word',
    color: CLS_COLOR[cls] || C.ink, ...style }}>
    {children}
  </code>
);

const CARET = <span style={{ display: 'inline-block', width: 13, height: 30, background: C.gate,
  verticalAlign: 'text-bottom', marginLeft: 2 }} />;

/* ---- scene components (frame-local) ---- */
const S1: React.FC<{ f: number }> = ({ f }) => {
  const cmd = 'g8s submit --provider cursor --model composer-2.5 -permission workspace_write';
  const n = Math.round(abs(f, [0, 72], [0, cmd.length], { easing: t => t, extrapolateRight: 'clamp' }));
  return (
    <Window title="operator@homelab — g8s session · real capture replay" scale={1} opacity={1}>
      <L cls="t-gate">$ {cmd.slice(0, n)}{f < 78 ? CARET : null}</L>
      {f > 82 && <L cls="t-dim">claiming… single-winner (SQLite WAL)</L>}
    </Window>
  );
};

const S2: React.FC<{ f: number }> = ({ f }) => {
  const rows = [
    '{ "kind": "task", "state": "QUEUED",',
    '  "data": { "task_id": "656d8e02-7832-405d-95c0-37dcc2836c2b",',
    '            "claim": "single-winner (SQLite WAL)", "timeout": "3000s" } }',
  ];
  return (
    <Window title="operator@homelab — g8s session" scale={1.02} opacity={1}>
      <L cls="t-gate">$ g8s submit --provider cursor --model composer-2.5 -permission workspace_write</L>
      {rows.map((r, i) => (
        <L key={i} cls="" style={{ opacity: abs(f, [14 + i * 12, 26 + i * 12], [0, 1], { extrapolateLeft: 'clamp', extrapolateRight: 'clamp' }) }}>{r}</L>
      ))}
      {f > 62 && <L cls="t-dim" style={{ opacity: abs(f, [62, 74], [0, 1]) }}>one attempt = one worktree = one receipt.</L>}
    </Window>
  );
};

const S3: React.FC<{ f: number }> = ({ f }) => {
  const inX = spring({ frame: f, fps: 30, config: { damping: 200 }, durationInFrames: 26 });
  return (
    <Window title="operator@homelab — g8s session" scale={0.94} opacity={1}>
      <L cls="t-gate">$ g8s receipt issue --path "internal/worker/**" --ttl 3300</L>
      <L cls="t-recv" style={{ opacity: abs(f, [18, 30], [0, 1], { extrapolateLeft: 'clamp', extrapolateRight: 'clamp' }) }}>
        receipt 5a41fd02-04af-4e0e-9a2d-6d7f0ea4bf78 minted
      </L>
      <div style={{ position: 'absolute', right: 120, top: 120, width: 470,
        transform: `translateX(${abs(inX, [0, 1], [900, 0])}px)`, background: C.paneHi,
        border: `1px solid ${C.faint}`, padding: '16px 18px', fontSize: 19 }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', color: C.gate, fontSize: 15,
          letterSpacing: 2, borderBottom: `1px solid ${C.faint}`, paddingBottom: 8, marginBottom: 8 }}>
          <span>Write receipt</span><span style={{ color: C.amber }}>single-use</span>
        </div>
        {[['allowed_paths', 'internal/worker/**'], ['issued_to', 'task 656d8e02'], ['ttl', '3300s · live']].map(([k, v]) => (
          <div key={k} style={{ display: 'flex', gap: 12, padding: '4px 0', fontSize: 17 }}>
            <span style={{ color: C.dim, minWidth: 140 }}>{k}</span><span style={{ color: C.ink, wordBreak: 'break-all' }}>{v}</span>
          </div>
        ))}
      </div>
    </Window>
  );
};

const S4: React.FC<{ f: number }> = ({ f }) => {
  const files = [
    ['store_claims.go', true], ['handle.go', true], ['lease_resilience_test.go', true],
    ['lease_renew.go', false],
  ];
  const IMPACT = 96; // refusal lands at frame 96 of the scene
  const flash = f >= IMPACT && f < IMPACT + 7;
  return (
    <Window title="operator@homelab — g8s session" scale={1} opacity={1}>
      <L cls="">$ worker --once → attempt claims 656d8e02 · worktree wt-cc7e6b8f</L>
      {files.map(([name, ok], i) => (
        <L key={name as string} cls={ok ? 't-dim' : 't-warn'}
          style={{ opacity: abs(f, [18 + i * 20, 30 + i * 20], [0, 1], { extrapolateLeft: 'clamp', extrapolateRight: 'clamp' }) }}>
          {'  edit internal/' + (ok ? 'controlplane|worker ' : 'worker/ ') + name}
          {ok ? '  — in scope' : <>  <span style={{ color: C.err }}>OUT OF SCOPE</span></>}
        </L>
      ))}
      {flash && (
        <div style={{ position: 'absolute', inset: 0, display: 'flex', alignItems: 'center', justifyContent: 'center',
          background: 'rgba(224,96,96,.05)' }}>
          <div style={{ border: `2px solid ${C.err}`, color: C.err, fontSize: 38, padding: '24px 44px',
            background: 'rgba(224,96,96,.07)', letterSpacing: 0 }}>
            E_DIFF_FIREWALL_OUT_OF_SCOPE
          </div>
        </div>
      )}
      {f > IMPACT + 7 && (
        <L cls="t-err" style={{ marginTop: 18 }}>gate refusal recorded — write refused, attempt failed</L>
      )}
    </Window>
  );
};

const S5: React.FC<{ f: number }> = ({ f }) => (
  <AbsoluteFill style={{ background: C.bg, display: 'flex', flexDirection: 'column',
    alignItems: 'center', justifyContent: 'center', gap: 22 }}>
    <div style={{ fontSize: 62, color: C.ink, opacity: abs(f, [6, 30], [0, 1]) }}>The gate refuses. Loudly.</div>
    <div style={{ fontSize: 24, color: C.dim, opacity: abs(f, [26, 46], [0, 1]) }}>
      a typed reason — not a mystery, not a silent skip
    </div>
    <div style={{ marginTop: 18, fontFamily: MONO, fontSize: 22, color: C.err,
      opacity: abs(f, [48, 60], [0, 1]) }}>
      worktree QUARANTINED in place — evidence preserved, never reaped
    </div>
  </AbsoluteFill>
);

const S6: React.FC<{ f: number }> = ({ f }) => (
  <Window title="operator@homelab — g8s session" scale={1} opacity={1}>
    <L cls="t-gate">$ g8s receipt issue --path "internal/controlplane/** internal/worker/**" --ttl 3300{f < 58 ? CARET : null}</L>
    {f > 60 && <L cls="t-recv" style={{ opacity: abs(f, [60, 72], [0, 1]) }}>fresh receipt minted — scope widened the right way: by a reviewed landing</L>}
  </Window>
);

const S7: React.FC<{ f: number }> = ({ f }) => {
  const gates = [
    ['layer_ownership', 'S/W/C/D lanes clean'],
    ['coverage_ratchet', '82.43% ≥ baseline 82.18%'],
    ['sast_triad', 'codeql + semgrep + gitleaks clean'],
  ];
  return (
    <Window title="operator@homelab — g8s session" scale={1} opacity={1}>
      <L cls="">$ worker --once → retry claims 656d8e02</L>
      {gates.map(([g, note], i) => {
        const cue = 24 + i * 30;
        const x = spring({ frame: f - cue, fps: 30, config: { damping: 200 }, durationInFrames: 22 });
        return (
          <L key={g} cls="t-gate" style={{ opacity: f > cue ? 1 : 0,
            transform: `translateX(${abs(x, [0, 1], [-40, 0])}px)` }}>
            {'  gate PASS  ' + g + '  '}
            <span style={{ color: C.dim }}>— {note}</span>
          </L>
        );
      })}
    </Window>
  );
};

const S8: React.FC<{ f: number }> = ({ f }) => (
  <Window title="operator@homelab — g8s session" scale={1} opacity={1}>
    <L cls="t-gate">$ g8s verify --task 656d8e02 --json</L>
    {['{ "ok": true, "verifier": "acceptance-matrix",', '  "checks": 6, "passed": 6,',
      '  "evidence": "worktree diff + gates" }'].map((r, i) => (
      <L key={i} cls="t-gate" style={{ opacity: abs(f, [16 + i * 14, 28 + i * 14], [0, 1], { extrapolateLeft: 'clamp', extrapolateRight: 'clamp' }) }}>{r}</L>
    ))}
  </Window>
);

const S9: React.FC<{ f: number }> = ({ f }) => (
  <Window title="operator@homelab — g8s session" scale={1} opacity={1}>
    <L cls="">$ git commit -m "fix(controlplane,provider): typed heartbeat-transient + Wait race"</L>
    <L cls="t-recv" style={{ marginTop: 8, opacity: abs(f, [30, 44], [0, 1], { extrapolateLeft: 'clamp', extrapolateRight: 'clamp' }) }}>
      Receipt: 656d8e02-7832-405d-95c0-37dcc2836c2b
    </L>
    {f > 54 && <L cls="t-gate" style={{ marginTop: 14, fontSize: 30, opacity: abs(f, [54, 68], [0, 1]) }}>
      → landed. lineage intact.</L>}
    {f > 74 && <L cls="t-dim" style={{ opacity: abs(f, [74, 86], [0, 1]) }}>the refusal made the fix safer.</L>}
  </Window>
);

const S10: React.FC<{ f: number }> = ({ f }) => (
  <AbsoluteFill style={{ background: C.bg, display: 'flex', flexDirection: 'column',
    alignItems: 'center', justifyContent: 'center', gap: 26 }}>
    <div style={{ fontFamily: MONO, fontSize: 26, color: C.gate, letterSpacing: 3,
      opacity: abs(f, [0, 18], [0, 1]) }}>g8s — the gatekeepers</div>
    <div style={{ fontSize: 74, color: C.ink, letterSpacing: -.5,
      opacity: abs(f, [14, 40], [0, 1]), transform: `translateY(${abs(f, [14, 40], [16, 0])}px)` }}>
      Gated. Receipted. Auditable. Signed.
    </div>
    <div style={{ fontFamily: MONO, fontSize: 24, color: C.recv,
      opacity: abs(f, [46, 62], [0, 1]) }}>github.com/tamld/g8s — install in 30 seconds</div>
  </AbsoluteFill>
);

/* ---- sequencer ---- */
const SHOTS: Array<[number, number, React.FC<{ f: number }>]> = [
  [0, 96, S1], [96, 192, S2], [192, 288, S3], [288, 426, S4], [426, 522, S5],
  [522, 618, S6], [618, 768, S7], [768, 864, S8], [864, 972, S9], [972, 1080, S10],
];

export const G8sReel: React.FC = () => {
  const frame = useCurrentFrame();
  const { durationInFrames } = useVideoConfig();
  const shot = SHOTS.find(([a, b]) => frame >= a && frame < b) || SHOTS[SHOTS.length - 1];
  const [a, b, Comp] = shot;
  const Comp2 = Comp as React.FC<{ f: number }>;
  // crossfade seam: 6f dip at shot boundaries via a black veil
  const toEdge = Math.min(frame - a, b - frame);
  const veil = toEdge < 4 ? abs(toEdge, [0, 4], [0.85, 0]) : 0;
  return (
    <AbsoluteFill style={{ background: C.bg, fontFamily: MONO }}>
      <Comp2 f={frame - a} />
      {veil > 0 && <AbsoluteFill style={{ background: C.bg, opacity: veil }} />}
      {/* faint blueprint grid overlay */}
      <AbsoluteFill style={{ pointerEvents: 'none', opacity: .1,
        backgroundImage: `linear-gradient(${C.faint} 1px, transparent 1px), linear-gradient(90deg, ${C.faint} 1px, transparent 1px)`,
        backgroundSize: '64px 64px' }} />
      {/* progress ticks */}
      <div style={{ position: 'absolute', left: 0, right: 0, bottom: 0, height: 8, display: 'flex' }}>
        {Array.from({ length: 36 }, (_, i) => (
          <div key={i} style={{ flex: 1, marginRight: i < 35 ? 2 : 0,
            background: frame / (durationInFrames / 36) >= i ? C.gate : 'transparent' }} />
        ))}
      </div>
    </AbsoluteFill>
  );
};
