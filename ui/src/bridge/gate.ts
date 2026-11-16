// Dev-only gate panel: a small overlay that proves the Go calls, the event
// stream, launching stock Chrome and the updater from the real UI. It is
// hidden by default. It shows with ?gate=1, or inside the Wails window when
// the app runs with LADILALA_GATE_AUTORUN (the self-test), and is plain DOM so
// it does not touch any view.
import { bridge } from './index'

function start() {
  const box = document.createElement('div')
  box.id = 'gate-panel'
  box.style.cssText =
    'position:fixed;right:12px;bottom:12px;z-index:99999;width:340px;font:12px/1.4 ui-monospace,monospace;' +
    'background:#111;color:#ddd;border:1px solid #555;border-radius:8px;padding:8px;opacity:.95'
  box.innerHTML =
    '<div style="display:flex;gap:4px;flex-wrap:wrap;margin-bottom:6px">' +
    ['ping', 'version', 'chrome', 'close', 'update', 'install']
      .map((b) => `<button data-b="${b}" style="padding:2px 6px;background:#333;color:#eee;border:1px solid #666;border-radius:4px">${b}</button>`)
      .join('') +
    '</div><div id="gate-tick"></div><pre id="gate-log" style="white-space:pre-wrap;margin:4px 0 0;max-height:180px;overflow:auto"></pre>'
  const log = box.querySelector<HTMLElement>('#gate-log')!
  const tickEl = box.querySelector<HTMLElement>('#gate-tick')!
  const say = (s: string) => {
    log.textContent = `${s}\n${log.textContent}`.slice(0, 2000)
  }
  const run = async (name: string, fn: () => Promise<unknown>) => {
    try {
      say(`${name}: ${JSON.stringify(await fn())}`)
    } catch (e) {
      say(`${name} ERROR: ${e}`)
    }
  }
  const actions: Record<string, () => Promise<unknown>> = {
    ping: () => bridge.ping(),
    version: () => bridge.version(),
    chrome: () => bridge.launchChrome(),
    close: () => bridge.closeBrowsers(),
    update: () => bridge.checkUpdate(),
    install: () => bridge.installUpdate(),
  }
  box.addEventListener('click', (e) => {
    const b = (e.target as HTMLElement).dataset.b
    if (b) void run(b, actions[b])
  })

  let ticks = 0
  bridge.onTick((n) => {
    ticks = n
    tickEl.textContent = `bridge: ${bridge.kind}  ticks: ${n}`
  })

  const mount = () => {
    document.body.appendChild(box)
    void startup()
  }

  async function startup() {
    const t0 = performance.timeOrigin
    const paint = await new Promise<number>((ok) => {
      const read = () => performance.getEntriesByName('first-contentful-paint')[0]?.startTime
      const v = read()
      if (v !== undefined) return ok(v)
      new PerformanceObserver((l, o) => {
        const e = l.getEntriesByName('first-contentful-paint')[0]
        if (e) {
          o.disconnect()
          ok(e.startTime)
        }
      }).observe({ type: 'paint', buffered: true })
    })
    const m = await bridge.ready(t0 + paint)
    say(`first paint ${m.firstPaintMs.toFixed(0)} ms after process start`)
    if (!(await bridge.autorun())) return
    const report: Record<string, unknown> = { firstPaintMs: m.firstPaintMs }
    const step = async (k: string, fn: () => Promise<unknown>) => {
      try {
        report[k] = await fn()
      } catch (e) {
        report[k] = { error: String(e) }
      }
      say(`${k}: ${JSON.stringify(report[k])}`)
    }
    await step('ping', () => bridge.ping())
    await step('version', () => bridge.version())
    await new Promise((r) => setTimeout(r, 3500))
    report.ticks = ticks
    await step('chrome', () => bridge.launchChrome())
    await step('update', () => bridge.checkUpdate())
    await bridge.writeReport(JSON.stringify(report, null, 2))
    say('report written')
    const upd = report.update as { available?: boolean } | undefined
    if (upd?.available && (await bridge.autoInstall())) {
      await step('install', () => bridge.installUpdate())
      await bridge.writeReport(JSON.stringify(report, null, 2))
    }
  }

  if (document.body) mount()
  else addEventListener('DOMContentLoaded', mount)
}

if (new URLSearchParams(location.search).has('gate')) start()
else if (bridge.kind === 'wails') void bridge.autorun().then((on) => { if (on) start() }).catch(() => {})
