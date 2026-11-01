// GET /cert : enrolment of a new device from a QR code (plans/07-first-contact-trust.md section 4.4).
// The QR code holds https://<base domain>/cert#<base64url DER of the root certificate>.
// The fragment never goes to the server. The page makes no network request after it loads.

export const CERT_HTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Install the care-pi certificate</title>
<link rel="stylesheet" href="/static/site.css">
<script src="/static/cert.js" defer></script>
</head>
<body>
<main>
<h1>Install the certificate of your care-pi device</h1>
<noscript><p class="warn">This page needs JavaScript. JavaScript reads the certificate from the link on this device only.</p></noscript>
<p id="status" class="muted">Reading the certificate from the link...</p>

<section id="details" hidden>
<div class="card">
<dl>
<dt>Name</dt><dd id="subject" class="mono"></dd>
<dt>Issuer</dt><dd id="issuer" class="mono"></dd>
<dt>Valid from</dt><dd id="not-before" class="mono"></dd>
<dt>Valid until</dt><dd id="not-after" class="mono"></dd>
<dt>Authority</dt><dd id="ca"></dd>
</dl>
</div>

<h2>Step 1: Compare the fingerprint</h2>
<p>Compare this SHA-256 fingerprint with the fingerprint on the console screen of your care-pi device.
If one character is different, stop. Do not install this certificate.</p>
<div class="card"><p id="fingerprint" class="mono fingerprint"></p></div>

<h2>Step 2: Download the certificate</h2>
<p>
<button id="dl-mobileconfig" type="button" disabled>iPhone, iPad, Mac (.mobileconfig)</button>
<button id="dl-crt" type="button" class="secondary" disabled>Android, Windows, ChromeOS (.crt)</button>
<button id="dl-pem" type="button" class="secondary" disabled>Linux, Firefox (.pem)</button>
</p>

<h2>Step 3: Trust the certificate</h2>
<ul>
<li><strong>iPhone, iPad:</strong> Open Settings. Tap <em>Profile Downloaded</em>, then <em>Install</em>.
Then go to Settings, General, About, Certificate Trust Settings, and turn on the care-pi certificate.</li>
<li><strong>Android:</strong> Open Settings, Security, Encryption and credentials, Install a certificate, CA certificate.
Select the .crt file. The names of these menus can be different on your phone.</li>
<li><strong>Windows:</strong> Open the .crt file. Select Install Certificate, Current User,
"Place all certificates in the following store", Trusted Root Certification Authorities.</li>
<li><strong>macOS:</strong> Open the .mobileconfig file. Then open System Settings, search for "Profiles", and install the profile.
Or open the .crt file in Keychain Access and set Trust to "Always Trust".</li>
<li><strong>Linux, Firefox:</strong> Import the .pem file as a certificate authority in the browser settings.</li>
</ul>
</section>

<section id="empty" hidden>
<p>This page needs a certificate in the link. Open the console of your care-pi device on a device that already trusts it.
Scan the QR code on the "Add a device" screen with this device.</p>
</section>

<p class="muted">Privacy: the certificate is in the part of the link after <code>#</code>.
Your browser does not send that part to this service, and this page sends nothing.</p>
</main>
</body>
</html>
`;
