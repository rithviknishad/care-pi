// GET / : the public page of the service (ASD-STE100 text). Static HTML; the script is /static/home.js.

function esc(text: string): string {
  return text.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
}

export function homeHtml(baseDomain: string): string {
  const d = esc(baseDomain);
  return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>care-pi DNS service</title>
<link rel="stylesheet" href="/static/site.css">
<script src="/static/home.js" defer></script>
</head>
<body>
<main>
<h1>care-pi DNS service</h1>
<p>This service gives each care-pi device a name in the form <code>&lt;id&gt;.${d}</code>.
The name points to the address of the device on the clinic network (LAN).
With this name, the device gets a public certificate from Let's Encrypt.
Then the browsers on the clinic network open Care with no certificate warning, and you do not install a certificate on each device.</p>

<h2>Find the clinics on your network</h2>
<div class="card">
<p>This list shows the clinics that registered from your public IP address and that agreed to be on the list.
A clinic is not on the list if it did not agree. This is the default.</p>
<button id="lookup" type="button">Show the clinics on my network</button>
<p id="lookup-status" class="muted" hidden></p>
<ul id="lookup-list" class="plain" hidden></ul>
</div>

<h2>Make sure that the address is your clinic</h2>
<p>The console of your care-pi device shows its id. Compare that id with the address in the address bar of your browser.
If the two are not the same, stop. Do not type a password. Another device on your network can show a page that looks like Care.</p>

<h2>Install a certificate from a QR code</h2>
<p>If your care-pi device uses its own certificate authority (offline mode), the console shows a QR code.
Scan the QR code with the new phone or tablet. The code opens <a href="/cert">/cert</a>, which shows the fingerprint of the certificate and lets you download it.
The certificate is in the part of the link after <code>#</code>. Your browser does not send that part to this service.</p>

<h2>What this service keeps</h2>
<ul>
<li>The id of each clinic and the hostname that the device sent.</li>
<li>The public key of the device and a hash of its ACME password.</li>
<li>The LAN address (IPv4, IPv6) of the device. This address is in public DNS.</li>
<li>The public IP address of the network at registration and at the last update, and the times of these events.</li>
</ul>
<p>The service does not receive patient data. Care traffic goes directly from the browser to the device on the clinic network. It never goes through this service.</p>

<footer class="muted">
<p>care-pi is open source: <a href="https://github.com/rithviknishad/care-pi">github.com/rithviknishad/care-pi</a>.
The API is in <code>services/dns/API.md</code>. Service health: <a href="/health">/health</a>.</p>
</footer>
</main>
</body>
</html>
`;
}

// The script of GET /. It asks /lookup (same origin) and shows the result. It uses textContent only.
export const HOME_JS = `"use strict";
(function () {
  var button = document.getElementById("lookup");
  var statusLine = document.getElementById("lookup-status");
  var list = document.getElementById("lookup-list");
  if (!button || !statusLine || !list) return;
  function show(text) {
    statusLine.textContent = text;
    statusLine.hidden = false;
  }
  button.addEventListener("click", function () {
    button.disabled = true;
    list.hidden = true;
    list.textContent = "";
    show("Searching...");
    fetch("/lookup", { headers: { accept: "application/json" }, cache: "no-store" })
      .then(function (res) {
        return res.json().then(function (body) { return { ok: res.ok, body: body }; });
      })
      .then(function (r) {
        if (!r.ok) {
          show("The service cannot show the list now (" + (r.body && r.body.error ? r.body.error : "error") + "). Try again later.");
          return;
        }
        var clinics = r.body.clinics || [];
        if (clinics.length === 0) {
          show("No clinic on the list for your public IP address (" + r.body.ip + ").");
          return;
        }
        show("Clinics for your public IP address (" + r.body.ip + "):");
        clinics.forEach(function (c) {
          var li = document.createElement("li");
          var a = document.createElement("a");
          a.href = "https://" + c.fulldomain + "/";
          a.textContent = c.fulldomain;
          a.rel = "noreferrer";
          li.appendChild(a);
          var when = document.createElement("span");
          when.className = "muted";
          when.textContent = " (registered " + c.registered_at + ")";
          li.appendChild(when);
          list.appendChild(li);
        });
        list.hidden = false;
      })
      .catch(function () {
        show("The service cannot show the list now. Try again later.");
      })
      .then(function () {
        button.disabled = false;
      });
  });
})();
`;
