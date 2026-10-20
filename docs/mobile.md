# Mobile app

`mobile/` packages the existing React app in a Capacitor iOS shell. Screens,
API clients, bearer tokens, space selection and money coercion remain in
`frontend/`; there is no second finance UI or calculation implementation.
Capacitor provides a path to Android with the same web bundle; an Android
project is not included.

The Xcode project links the official Capacitor 8.0.0 Swift package directly,
including its native JavaScript bridge and HTTP implementation. The small
native toolbar, status bar, safe-area constraints and launch storyboard need
no JavaScript plugins. Mobile build scripts use the frontend's installed Vite
and React dependencies; there is no second npm dependency tree. `sync` copies
the bundle and configuration into the native project's resources. It does not
run the Capacitor CLI or manage third-party native plugins.

## Server and sign-in

On first launch enter the instance root URL, for example
`http://10.10.20.101:8100`. An optional `/api` suffix is accepted and normalized.
The app checks the public first-account endpoint without credentials before
saving. URLs containing credentials, queries, fragments or other paths are
rejected. Instances mounted under a subpath are not supported.

The server is saved on the device, and the runtime API base is set before
importing the shared app. Web builds continue to use `VITE_API_BASE` or `/api`.
Sign in with the existing password and optional TOTP code. The same API client
sets `Authorization` and `X-Space-Id`, parses money and handles expired sessions.
Tap **Server** in the native toolbar to edit the URL, including when the saved
server is unreachable. Changing it clears the previous session, space choices
and other `agentifi.*` preferences, then reloads to discard all query caches.
Cancel keeps the current server and session.

The bundled UI stays on Capacitor's local origin. `CapacitorHttp` routes shared
`fetch` calls through native networking so no backend CORS change is needed.
`Info.plist` allows arbitrary native HTTP loads because people can enter any
LAN IP address; a fixed domain exception cannot cover them. It also declares
the Local Network usage description. Allow the permission when iOS asks; if
denied, enable it in Settings → Apps → Agentifi → Local Network. HTTP transmits
tokens and financial data unencrypted: use it on a trusted LAN, and use HTTPS
for remote access. Self-signed HTTPS certificates are not bypassed.

Browser SSO/OIDC callbacks, passkeys and web push have not been adapted to the
native origin; use password/TOTP sign-in. Native deep links, associated domains
and native notifications would be separate work. Large file uploads/downloads
and connector popups also need device testing. Tokens retain the shared app's
localStorage persistence; this shell does not add Keychain storage or biometrics.

## Build and run on a Mac

Install Node 22+ and Xcode 26+ with its iOS SDK and command line tools. Open
Xcode once to accept the license and install a simulator. Swift Package Manager
resolves Capacitor; CocoaPods is not needed. From the repository root:

```sh
cd frontend
npm ci
npm run build
cd ../mobile
npm run ios
```

`ios` builds the mobile web bundle, copies resources, and opens
`mobile/ios/App/App.xcodeproj`. In Xcode:

1. Let the Capacitor and Cordova package products resolve.
2. Select the **App** target and **Signing & Capabilities**. Choose your Apple
   development team and a unique bundle identifier (the placeholder is
   `org.agentifi.app`). Automatic signing is enabled.
3. Select the **App** scheme and an iPhone simulator, then Run. For a physical
   iPhone, enable Developer Mode, pair/trust it, select it as the destination
   and let Xcode provision the app. The phone needs access to the server's LAN
   or VPN; `localhost` refers to the phone itself.
4. Enter the server URL, allow Local Network access, and sign in. Check launch,
   relaunch, expired sessions, server changes, keyboard focus, portrait and
   landscape safe areas, bottom navigation, dialogs, imports and attachments.

After shared frontend changes, run `cd mobile && npm run sync`, then rebuild
in Xcode. Do not run `cap add ios` over this project. Replace the placeholder
icon in `mobile/ios/App/App/Assets.xcassets/AppIcon.appiconset` before distributing.
The launch storyboard and chrome use the shared app's default dark palette.
Building, device tests, signing, archiving and TestFlight/App Store distribution
all require macOS/Xcode and the appropriate Apple account.

For a simulator build from the command line after syncing:

```sh
xcodebuild -project ios/App/App.xcodeproj -scheme App \
  -configuration Debug -sdk iphonesimulator \
  -destination 'generic/platform=iOS Simulator' \
  -derivedDataPath ios/App/build CODE_SIGNING_ALLOWED=NO build
```

## Linux checks

Install `frontend/` dependencies, then from the repository root:

```sh
cd frontend
npm run build
npm run lint
npm run test -- --run
npm run layout
cd ../mobile
npm run sync
npm run lint
npm test
npm run test:browser
```

The shared layout suite covers 320px and larger widths. Mobile browser tests
serve the built bundle through request interception and use invented API
answers; they check first-run gating, runtime API selection, relaunch and the
native toolbar's settings event. They require Playwright Chromium installed
through `cd frontend && npx playwright install chromium`. `npm run dev` in
`mobile/` previews setup in a browser; browsers still enforce CORS, unlike the
native HTTP bridge.

Linux can bundle and copy web resources, run lint and tests, parse the project
and plist, and check Swift syntax. It cannot compile UIKit, validate Swift
framework linking, sign the app or confirm iOS networking/permissions. Those
checks remain part of the Mac/device workflow above.

References: [Capacitor environment setup](https://capacitorjs.com/docs/getting-started/environment-setup),
[native HTTP](https://capacitorjs.com/docs/apis/http),
[official Swift package](https://github.com/ionic-team/capacitor-swift-pm/tree/8.0.0),
and [Apple ATS exceptions](https://developer.apple.com/documentation/bundleresources/information-property-list/nsapptransportsecurity).
