package browser

import "github.com/playwright-community/playwright-go"

// applyDesktopConsistency injects a script into a browser context before any
// page loads so the properties the browser reports agree with the desktop
// Chrome it names in its user agent. The patches remove contradictions
// Playwright introduces — navigator.webdriver set to true, window.chrome absent
// in headless, navigator.plugins emptied, userAgentData missing — which some
// sign-in pages reject as inconsistent with the user agent. None adds a
// capability; each makes the browser consistent with what it claims to be.
//
// The script is adapted from puppeteer-extra-plugin-stealth
// (https://github.com/berstend/puppeteer-extra), under the MIT License below.
// The userAgentData section derives its brands from navigator.userAgent at
// runtime so it matches a profile's recorded UA.
//
//	Copyright (c) 2019 berstend <github@berstend.com>
//
//	Permission is hereby granted, free of charge, to any person obtaining a
//	copy of this software and associated documentation files (the
//	"Software"), to deal in the Software without restriction, including
//	without limitation the rights to use, copy, modify, merge, publish,
//	distribute, sublicense, and/or sell copies of the Software, and to permit
//	persons to whom the Software is furnished to do so, subject to the
//	following conditions:
//
//	The above copyright notice and this permission notice shall be included
//	in all copies or substantial portions of the Software.
//
//	THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS
//	OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF
//	MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN
//	NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM,
//	DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR
//	OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE
//	USE OR OTHER DEALINGS IN THE SOFTWARE.
func applyDesktopConsistency(context playwright.BrowserContext) {
	_ = context.AddInitScript(playwright.Script{
		Content: playwright.String(desktopConsistencyScript),
	})
}

const desktopConsistencyScript = `(() => {
  // --- navigator.webdriver ---------------------------------------------------
  // Playwright sets this via CDP. Real Chrome does not expose it at all:
  // both the value and the property must be gone, because a page may check
  // 'webdriver' in navigator (the in operator), not just the value. Deleting
  // from the prototype is enough — accessing a non-existent property returns
  // undefined, which is the correct answer.
  try {
    delete Object.getPrototypeOf(navigator).webdriver;
    if ('webdriver' in navigator) {
      delete navigator.webdriver;
    }
  } catch (_) {}

  // --- window.chrome ---------------------------------------------------------
  // Headless Chromium omits the object every Chrome extension and many sites
  // expect to find.
  if (!window.chrome) {
    Object.defineProperty(window, 'chrome', {
      value: {
        app: {
          isInstalled: false,
          InstallState: {
            DISABLED: 'disabled',
            INSTALLED: 'installed',
            NOT_INSTALLED: 'not_installed',
          },
          RunningState: {
            CANNOT_RUN: 'cannot_run',
            READY_TO_RUN: 'ready_to_run',
            RUNNING: 'running',
          },
          getDetails: () => null,
          getIsInstalled: () => false,
          installState: (cb) => { if (cb) cb('not_installed'); },
          runningState: () => 'cannot_run',
        },
        csi: () => ({}),
        loadTimes: () => ({}),
        runtime: {
          connect: () => ({ onDisconnect: { addListener: () => {} } }),
          sendMessage: () => {},
          id: undefined,
          PlatformOs: {
            MAC: 'mac', WIN: 'win', ANDROID: 'android',
            CROS: 'cros', LINUX: 'linux', OPENBSD: 'openbsd',
          },
          PlatformArch: {
            ARM: 'arm', X86_32: 'x86-32', X86_64: 'x86-64',
            MIPS: 'mips', MIPS64: 'mips64',
          },
          PlatformNaclArch: {
            ARM: 'arm', X86_32: 'x86-32', X86_64: 'x86-64',
            MIPS: 'mips', MIPS64: 'mips64',
          },
          RequestUpdateCheckStatus: {
            THROTTLED: 'throttled', NO_UPDATE: 'no_update',
            UPDATE_AVAILABLE: 'update_available',
          },
          OnInstalledReason: {
            INSTALL: 'install', UPDATE: 'update',
            CHROME_UPDATE: 'chrome_update',
            SHARED_MODULE_UPDATE: 'shared_module_update',
          },
          OnRestartRequiredReason: {
            APP_UPDATE: 'app_update', OS_UPDATE: 'os_update',
            PERIODIC: 'periodic',
          },
        },
      },
      writable: true,
      configurable: true,
    });
  }

  // --- navigator.plugins -----------------------------------------------------
  // Headless Chromium may report an empty plugin array. Real Chrome has the
  // PDF viewer registered five ways.
  if (navigator.plugins.length === 0) {
    try {
      const makeMime = (type, suffixes, desc, plugin) => {
        const m = Object.create(MimeType.prototype);
        Object.defineProperties(m, {
          type: { get: () => type },
          suffixes: { get: () => suffixes },
          description: { get: () => desc },
          enabledPlugin: { get: () => plugin },
        });
        return m;
      };
      const makePlugin = (name, desc, filename, types) => {
        const p = Object.create(Plugin.prototype);
        const mimes = types.map((t) => makeMime(t.type, t.suffixes, t.desc, p));
        Object.defineProperties(p, {
          name: { get: () => name },
          description: { get: () => desc },
          filename: { get: () => filename },
          length: { get: () => mimes.length },
        });
        mimes.forEach((m, i) => {
          Object.defineProperty(p, i, { get: () => m });
        });
        return { plugin: p, mimes };
      };

      const pdf = { type: 'application/pdf', suffixes: 'pdf', desc: 'Portable Document Format' };
      const pdfBlank = { type: 'application/pdf', suffixes: 'pdf', desc: '' };
      const entries = [
        makePlugin('PDF Viewer', 'Portable Document Format', 'internal-pdf-viewer', [pdf]),
        makePlugin('Chrome PDF Viewer', 'Portable Document Format', 'internal-pdf-viewer', [pdfBlank]),
        makePlugin('Chromium PDF Viewer', 'Portable Document Format', 'internal-pdf-viewer', [pdfBlank]),
        makePlugin('Microsoft Edge PDF Viewer', 'Portable Document Format', 'internal-pdf-viewer', [pdfBlank]),
        makePlugin('WebKit built-in PDF', 'Portable Document Format', 'internal-pdf-viewer', [pdfBlank]),
      ];
      const allPlugins = entries.map((e) => e.plugin);
      const allMimes = entries.flatMap((e) => e.mimes);

      Object.defineProperty(navigator, 'plugins', {
        get: () => {
          const arr = Object.create(PluginArray.prototype);
          allPlugins.forEach((p, i) => Object.defineProperty(arr, i, { get: () => p }));
          Object.defineProperties(arr, {
            length: { get: () => allPlugins.length },
            item: { value: (i) => allPlugins[i] || null },
            namedItem: { value: (n) => allPlugins.find((p) => p.name === n) || null },
            refresh: { value: () => {} },
          });
          return arr;
        },
        configurable: true,
      });
      Object.defineProperty(navigator, 'mimeTypes', {
        get: () => {
          const arr = Object.create(MimeTypeArray.prototype);
          allMimes.forEach((m, i) => Object.defineProperty(arr, i, { get: () => m }));
          Object.defineProperties(arr, {
            length: { get: () => allMimes.length },
            item: { value: (i) => allMimes[i] || null },
            namedItem: { value: (t) => allMimes.find((m) => m.type === t) || null },
          });
          return arr;
        },
        configurable: true,
      });
    } catch (_) {}
  }

  // --- navigator.permissions -------------------------------------------------
  // Headless Chromium returns "prompt" for the notifications permission while
  // Notification.permission says "denied" — two answers that disagree.
  try {
    const origQuery = navigator.permissions.query.bind(navigator.permissions);
    navigator.permissions.query = (desc) => {
      if (desc.name === 'notifications') {
        return Promise.resolve({ state: Notification.permission, onchange: null });
      }
      return origQuery(desc);
    };
  } catch (_) {}

  // --- navigator.userAgentData -----------------------------------------------
  // Derive brands from the UA string so the two are consistent regardless of
  // which version the profile recorded.
  try {
    const uaMatch = navigator.userAgent.match(/Chrome\/([\d.]+)/);
    const fullVersion = uaMatch ? uaMatch[1] : '0.0.0.0';
    const major = fullVersion.split('.')[0];
    const platform = /Linux/.test(navigator.userAgent) ? 'Linux'
      : /Mac/.test(navigator.userAgent) ? 'macOS'
      : /Windows/.test(navigator.userAgent) ? 'Windows'
      : 'Linux';

    const brands = [
      { brand: 'Chromium', version: major },
      { brand: 'Google Chrome', version: major },
      { brand: 'Not_A Brand', version: '24' },
    ];
    const fullBrands = [
      { brand: 'Chromium', version: fullVersion },
      { brand: 'Google Chrome', version: fullVersion },
      { brand: 'Not_A Brand', version: '24.0.0.0' },
    ];

    const uaData = {
      brands,
      mobile: false,
      platform,
      getHighEntropyValues: () => Promise.resolve({
        architecture: 'x86',
        bitness: '64',
        brands,
        fullVersionList: fullBrands,
        mobile: false,
        model: '',
        platform,
        platformVersion: '6.1.0',
        uaFullVersion: fullVersion,
        wow64: false,
      }),
      toJSON() {
        return { brands: this.brands, mobile: this.mobile, platform: this.platform };
      },
    };
    Object.defineProperty(navigator, 'userAgentData', {
      get: () => uaData,
      configurable: true,
    });
  } catch (_) {}

  // --- navigator.platform ----------------------------------------------------
  // Make the platform consistent with the user agent, so an old profile whose
  // pinned UA claims Windows does not contradict itself on this read.
  try {
    const expected = /Linux/.test(navigator.userAgent) ? 'Linux x86_64'
      : /Mac/.test(navigator.userAgent) ? 'MacIntel'
      : /Windows/.test(navigator.userAgent) ? 'Win32'
      : 'Linux x86_64';
    if (navigator.platform !== expected) {
      Object.defineProperty(navigator, 'platform', {
        get: () => expected,
        configurable: true,
      });
    }
  } catch (_) {}

  // --- WebGL renderer --------------------------------------------------------
  // SwiftShader (the software GPU containers use) reports "Google SwiftShader"
  // as the renderer string, which is a reliable signal that the browser is
  // running without a real GPU. Override getParameter to return a common
  // integrated GPU instead.
  try {
    const getParam = WebGLRenderingContext.prototype.getParameter;
    const UNMASKED_VENDOR = 0x9245;
    const UNMASKED_RENDERER = 0x9246;
    WebGLRenderingContext.prototype.getParameter = function (param) {
      if (param === UNMASKED_VENDOR) return 'Intel Inc.';
      if (param === UNMASKED_RENDERER) return 'Intel Iris OpenGL Engine';
      return getParam.call(this, param);
    };
    if (typeof WebGL2RenderingContext !== 'undefined') {
      const getParam2 = WebGL2RenderingContext.prototype.getParameter;
      WebGL2RenderingContext.prototype.getParameter = function (param) {
        if (param === UNMASKED_VENDOR) return 'Intel Inc.';
        if (param === UNMASKED_RENDERER) return 'Intel Iris OpenGL Engine';
        return getParam2.call(this, param);
      };
    }
  } catch (_) {}

  // --- navigator.hardwareConcurrency -----------------------------------------
  // A container may report a single core. Real machines have at least 4.
  try {
    if (navigator.hardwareConcurrency < 4) {
      Object.defineProperty(navigator, 'hardwareConcurrency', {
        get: () => 8,
        configurable: true,
      });
    }
  } catch (_) {}

  // --- navigator.deviceMemory ------------------------------------------------
  // Containers often report low memory. Override to a common desktop value.
  try {
    if (!navigator.deviceMemory || navigator.deviceMemory < 4) {
      Object.defineProperty(navigator, 'deviceMemory', {
        get: () => 8,
        configurable: true,
      });
    }
  } catch (_) {}

  // --- navigator.connection --------------------------------------------------
  // Headless browsers may expose a NetworkInformation with unusual values.
  try {
    if (navigator.connection) {
      Object.defineProperty(navigator.connection, 'rtt', {
        get: () => 50,
        configurable: true,
      });
    }
  } catch (_) {}
})();`
