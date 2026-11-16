// Ladilala injector. One script for pages, iframes and workers; the Go side
// replaces the placeholder on the next line with the per-profile config JSON.
// Ideas (native-looking wrappers, toString map, canvas noise via a scratch
// canvas) follow apify/fingerprint-suite, Apache-2.0; see NOTICE.
(() => {
  'use strict';
  const cfg = __LADILALA_CFG__;
  const G = globalThis;
  const isWorker = typeof window === 'undefined';
  const defProp = Object.defineProperty;
  const gopd = Object.getOwnPropertyDescriptor;
  const apply = Reflect.apply;

  // ---- native-looking helpers -------------------------------------------
  const nativeToString = Function.prototype.toString;
  // Wrappers carry this marker in their source. Function.prototype.toString
  // finds it in the source text instead of a per-realm WeakMap, so a wrapper
  // still reads as native when another realm (an iframe, a detector's phantom
  // iframe) asks its own, differently patched, toString.
  const MARK = '/*@@wm*/';
  const cleanStack = (e) => {
    // The top frame of a native error that passed through a wrapper is ours.
    try {
      if (e && typeof e.stack === 'string') {
        const lines = e.stack.split('\n');
        const i = lines.findIndex((l) => /^\s+at /.test(l));
        if (i >= 0 && /\(<anonymous>:\d+:\d+\)/.test(lines[i])) lines.splice(i, 1);
        e.stack = lines.join('\n');
      }
    } catch (_) {}
    return e;
  };
  const named = (fn, name, len) => {
    defProp(fn, 'name', { value: name, configurable: true });
    defProp(fn, 'length', { value: len, configurable: true });
    return fn;
  };
  const toStr = {
    toString() {
      /*@@wm*/
      let src;
      try { src = apply(nativeToString, this, []); } catch (e) { throw cleanStack(e); }
      return src.indexOf(MARK) >= 0 ? 'function ' + this.name + '() { [native code] }' : src;
    },
  }.toString;
  named(toStr, 'toString', 0);
  defProp(Function.prototype, 'toString', { value: toStr, writable: true, configurable: true, enumerable: false });

  // Replace method `key` of `proto` with fn(native, thisArg, args).
  const wrapMethod = (proto, key, impl) => {
    if (!proto) return;
    const d = gopd(proto, key);
    if (!d || typeof d.value !== 'function') return;
    const native = d.value;
    const w = { [key](...args) {
      /*@@wm*/
      try { return impl(native, this, args); } catch (e) { throw cleanStack(e); }
    } }[key];
    named(w, key, native.length);
    defProp(proto, key, { value: w, writable: d.writable, configurable: d.configurable, enumerable: d.enumerable });
  };
  // Replace getter `key` of `proto`; the native getter still runs first so
  // illegal-invocation behaviour is preserved.
  const wrapGetter = (proto, key, value) => {
    if (!proto) return;
    const d = gopd(proto, key);
    if (!d || typeof d.get !== 'function') return;
    const nativeGet = d.get;
    const name = 'get ' + key;
    const g = { [name]() {
      /*@@wm*/
      try { const real = apply(nativeGet, this, []); return value(real); } catch (e) { throw cleanStack(e); }
    } }[name];
    named(g, name, 0);
    defProp(proto, key, { get: g, set: d.set, configurable: d.configurable, enumerable: d.enumerable });
  };

  // ---- seeded noise ------------------------------------------------------
  const S0 = cfg.seed[0] >>> 0, S1 = cfg.seed[1] >>> 0;
  const mix = (salt, a, b) => {
    let h = (S0 ^ Math.imul(salt, 0x9e3779b1)) >>> 0;
    h = Math.imul(h ^ (a >>> 0), 0x85ebca6b); h ^= h >>> 13;
    h = Math.imul(h ^ (b >>> 0) ^ S1, 0xc2b2ae35); h ^= h >>> 16;
    return h >>> 0;
  };
  const noisePixels = (d, salt) => {
    for (let i = 0, p = 0; i < d.length; i += 4, p++) {
      if (d[i + 3] === 0) continue;
      const h = mix(salt, p, d.length);
      if ((h & 7) === 0) d[i + (h >>> 3) % 3] ^= 1;
    }
  };

  // ---- navigator (page and worker) --------------------------------------
  const navProto = isWorker ? G.WorkerNavigator && G.WorkerNavigator.prototype : G.Navigator && G.Navigator.prototype;
  wrapGetter(navProto, 'hardwareConcurrency', () => cfg.hardwareThreads);
  wrapGetter(navProto, 'deviceMemory', () => cfg.deviceMemory);
  wrapGetter(navProto, 'platform', () => cfg.navigatorPlatform);
  const langs = Object.freeze(cfg.languages.slice());
  if (isWorker) { // CDP user-agent emulation does not reach shared/service workers
    wrapGetter(navProto, 'userAgent', () => cfg.userAgent);
    wrapGetter(navProto, 'appVersion', () => cfg.userAgent.replace(/^Mozilla\//, ''));
  }
  wrapGetter(navProto, 'languages', () => langs);
  wrapGetter(navProto, 'language', () => langs[0]);

  // ---- screen (page only) -----------------------------------------------
  if (!isWorker && G.Screen) {
    const sp = G.Screen.prototype;
    wrapGetter(sp, 'width', () => cfg.screenWidth);
    wrapGetter(sp, 'height', () => cfg.screenHeight);
    wrapGetter(sp, 'availWidth', () => cfg.screenWidth);
    wrapGetter(sp, 'availHeight', () => cfg.screenHeight - cfg.taskbar);
    wrapGetter(sp, 'colorDepth', () => 24);
    wrapGetter(sp, 'pixelDepth', () => 24);
  }

  // ---- canvas ------------------------------------------------------------
  if (cfg.canvas && (G.HTMLCanvasElement || G.OffscreenCanvas)) {
    const C2D = G.CanvasRenderingContext2D && G.CanvasRenderingContext2D.prototype;
    const OC2D = G.OffscreenCanvasRenderingContext2D && G.OffscreenCanvasRenderingContext2D.prototype;
    const nativeGID = C2D ? gopd(C2D, 'getImageData').value : OC2D ? gopd(OC2D, 'getImageData').value : null;
    const nativeGetContext = gopd((G.HTMLCanvasElement || G.OffscreenCanvas).prototype, 'getContext').value;
    const noisyGID = (native, ctx, args) => {
      const img = apply(native, ctx, args);
      noisePixels(img.data, 1);
      return img;
    };
    wrapMethod(C2D, 'getImageData', noisyGID);
    wrapMethod(OC2D, 'getImageData', noisyGID);

    // Remember the 2D context of each canvas, so a read can use the canvas's
    // own pixels (getImageData) instead of a drawImage copy: a copy made soon
    // after launch can come back blank (measured on Windows Chrome 157).
    const ctxOf = new WeakMap();
    const track = (native, el, args) => {
      const r = apply(native, el, args);
      if (r && args[0] === '2d') ctxOf.set(el, r);
      return r;
    };
    wrapMethod(G.HTMLCanvasElement && G.HTMLCanvasElement.prototype, 'getContext', track);
    wrapMethod(G.OffscreenCanvas && G.OffscreenCanvas.prototype, 'getContext', track);

    const mkCanvas = (w, h) => {
      const c = isWorker ? new G.OffscreenCanvas(w, h) : G.document.createElement('canvas');
      c.width = w; c.height = h;
      return c;
    };
    // Return a noised copy of `src`, or null to read src itself.
    const scratch = (src) => {
      const w = src.width, h = src.height;
      if (!w || !h) return null;
      const c = mkCanvas(w, h);
      const x = apply(nativeGetContext, c, ['2d']);
      const own = ctxOf.get(src);
      let img;
      if (own) {
        img = apply(nativeGID, own, [0, 0, w, h]);
      } else { // WebGL or a context-less canvas: copy it through drawImage
        x.drawImage(src, 0, 0);
        img = apply(nativeGID, x, [0, 0, w, h]);
      }
      noisePixels(img.data, 1);
      x.putImageData(img, 0, 0);
      return c;
    };
    if (!isWorker && G.HTMLCanvasElement) {
      const hp = G.HTMLCanvasElement.prototype;
      wrapMethod(hp, 'toDataURL', (native, el, args) => {
        let s = null;
        try { s = scratch(el); } catch (_) {}
        return apply(native, s || el, args);
      });
      wrapMethod(hp, 'toBlob', (native, el, args) => {
        let s = null;
        try { s = scratch(el); } catch (_) {}
        return apply(native, s || el, args);
      });
    }
    if (G.OffscreenCanvas) {
      wrapMethod(G.OffscreenCanvas.prototype, 'convertToBlob', (native, el, args) => {
        let s = null;
        try { s = scratch(el); } catch (_) {}
        return apply(native, s || el, args);
      });
    }
  }

  // ---- WebGL (page and worker) ------------------------------------------
  if (cfg.webgl) {
    const UNMASKED_VENDOR = 0x9245, UNMASKED_RENDERER = 0x9246;
    const table = new Map();
    for (const [k, kind, v] of cfg.webglParams) table.set(k, [kind, v]);
    const mk = (kind, v) => kind === 'i32' ? new Int32Array(v) : kind === 'f32' ? new Float32Array(v) : v;
    const getParam = (native, gl, args) => {
      const real = apply(native, gl, args);
      const p = args[0];
      if (p === UNMASKED_VENDOR) return cfg.gpuVendor;
      if (p === UNMASKED_RENDERER) return cfg.gpuRenderer;
      const t = table.get(p);
      return t ? mk(t[0], t[1]) : real;
    };
    const readPix = (native, gl, args) => {
      const r = apply(native, gl, args);
      const buf = args[6];
      if (buf instanceof Uint8Array || buf instanceof Uint8ClampedArray) {
        for (let i = 0; i < buf.length; i++) {
          const h = mix(2, i, buf.length);
          if ((h & 15) === 0) buf[i] ^= 1;
        }
      }
      return r;
    };
    for (const name of ['WebGLRenderingContext', 'WebGL2RenderingContext']) {
      const proto = G[name] && G[name].prototype;
      wrapMethod(proto, 'getParameter', getParam);
      wrapMethod(proto, 'readPixels', readPix);
    }
  }

  // ---- audio (page only; AudioBuffer is not exposed to workers) ----------
  if (cfg.audio && G.AudioBuffer) {
    const done = new WeakSet();
    wrapMethod(G.AudioBuffer.prototype, 'getChannelData', (native, buf, args) => {
      const a = apply(native, buf, args);
      if (!done.has(a)) {
        done.add(a);
        for (let i = 0; i < a.length; i += 100) {
          const h = mix(3, i, a.length);
          if (a[i] !== 0) a[i] *= 1 + ((h & 0xffff) / 65536 - 0.5) * 1e-6;
        }
      }
      return a;
    });
    wrapMethod(G.AudioBuffer.prototype, 'copyFromChannel', (native, buf, args) => {
      const r = apply(native, buf, args);
      const dst = args[0], ch = args[1] | 0, off = args[2] | 0;
      if (dst && dst.length) {
        // Same noise as getChannelData would add at the same absolute index.
        for (let i = 0; i < dst.length; i++) {
          const abs = off + i;
          if (abs % 100 === 0 && dst[i] !== 0) dst[i] *= 1 + ((mix(3, abs, buf.length) & 0xffff) / 65536 - 0.5) * 1e-6;
        }
      }
      return r;
    });
    if (G.AnalyserNode) {
      for (const key of ['getFloatFrequencyData', 'getFloatTimeDomainData']) {
        wrapMethod(G.AnalyserNode.prototype, key, (native, node, args) => {
          const r = apply(native, node, args);
          const a = args[0];
          if (a && a.length) for (let i = 0; i < a.length; i += 7) a[i] += ((mix(4, i, a.length) & 0xffff) / 65536 - 0.5) * 1e-4;
          return r;
        });
      }
    }
  }

  // ---- client rects (page only) -----------------------------------------
  if (cfg.clientRects && !isWorker) {
    // Layout works in 1/64 px units; jitter in whole units so values stay
    // plausible. Position is untouched, so right/bottom stay x+width/y+height.
    const Q = 1 / 64;
    const jitter = (r) => {
      if (!r || (!r.width && !r.height)) return r;
      const q = (k, v) => { const h = mix(5 + k, Math.round(v * 64), 0); return (h & 7) === 0 ? ((h & 8) ? Q : -Q) : 0; };
      r.width += q(2, r.width); r.height += q(3, r.height);
      return r;
    };
    for (const proto of [G.Element && G.Element.prototype, G.Range && G.Range.prototype]) {
      wrapMethod(proto, 'getBoundingClientRect', (native, el, args) => jitter(apply(native, el, args)));
      wrapMethod(proto, 'getClientRects', (native, el, args) => {
        const l = apply(native, el, args);
        for (let i = 0; i < l.length; i++) jitter(l[i]);
        return l;
      });
    }
  }
})();
