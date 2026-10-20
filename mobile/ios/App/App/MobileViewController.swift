import UIKit
import Capacitor

class MobileViewController: UIViewController {
    private let browser = CAPBridgeViewController()
    private let chromeColor = UIColor(red: 14 / 255.0, green: 15 / 255.0, blue: 19 / 255.0, alpha: 1)

    override var preferredStatusBarStyle: UIStatusBarStyle { .lightContent }

    override func viewDidLoad() {
        super.viewDidLoad()
        view.backgroundColor = chromeColor

        let toolbar = UIToolbar()
        toolbar.translatesAutoresizingMaskIntoConstraints = false
        toolbar.barStyle = .black
        toolbar.isTranslucent = false
        toolbar.barTintColor = chromeColor
        toolbar.tintColor = .white
        toolbar.items = [
            UIBarButtonItem(title: "Agentifi", style: .plain, target: nil, action: nil),
            UIBarButtonItem(barButtonSystemItem: .flexibleSpace, target: nil, action: nil),
            UIBarButtonItem(title: "Server", style: .plain, target: self, action: #selector(changeServer))
        ]
        toolbar.items?.first?.isEnabled = false
        view.addSubview(toolbar)

        addChild(browser)
        browser.view.translatesAutoresizingMaskIntoConstraints = false
        view.addSubview(browser.view)
        browser.didMove(toParent: self)

        // Constrain the entire WebView so fixed headers, dialogs and tabs share the safe area.
        NSLayoutConstraint.activate([
            toolbar.topAnchor.constraint(equalTo: view.safeAreaLayoutGuide.topAnchor),
            toolbar.leadingAnchor.constraint(equalTo: view.safeAreaLayoutGuide.leadingAnchor),
            toolbar.trailingAnchor.constraint(equalTo: view.safeAreaLayoutGuide.trailingAnchor),
            toolbar.heightAnchor.constraint(equalToConstant: 44),
            browser.view.topAnchor.constraint(equalTo: toolbar.bottomAnchor),
            browser.view.leadingAnchor.constraint(equalTo: view.safeAreaLayoutGuide.leadingAnchor),
            browser.view.trailingAnchor.constraint(equalTo: view.safeAreaLayoutGuide.trailingAnchor),
            browser.view.bottomAnchor.constraint(equalTo: view.safeAreaLayoutGuide.bottomAnchor)
        ])
    }

    @objc private func changeServer() {
        browser.webView?.evaluateJavaScript("window.dispatchEvent(new Event('agentifi:server-settings'))", completionHandler: nil)
    }
}
