// The script of GET /cert. It is plain browser JavaScript (ES2017) in a string, so the served bytes are
// exactly this text. test/node/cert-client.test.ts runs this script in Node with a small DOM stub.
//
// The script reads the certificate from location.hash, computes its SHA-256 fingerprint, shows the
// names and dates, and builds the .crt, .pem, and .mobileconfig files on this device. It makes no network request.

export const CERT_JS = String.raw`"use strict";
(function () {
  var MAX_FRAGMENT = 16384;
  var NAMES = { "2.5.4.3": "CN", "2.5.4.6": "C", "2.5.4.7": "L", "2.5.4.8": "ST", "2.5.4.10": "O", "2.5.4.11": "OU" };

  function decodeB64(text) {
    var t = text;
    try { t = decodeURIComponent(t); } catch (e) { /* keep the raw text */ }
    t = t.replace(/\s+/g, "").replace(/-/g, "+").replace(/_/g, "/").replace(/=+$/, "");
    if (t.length === 0 || !/^[A-Za-z0-9+\/]*$/.test(t) || t.length % 4 === 1) throw new Error("The link does not hold base64 data.");
    while (t.length % 4 !== 0) t += "=";
    var bin = atob(t);
    var out = new Uint8Array(bin.length);
    for (var i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
    return out;
  }

  function toB64(bytes) {
    var bin = "";
    for (var i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i]);
    return btoa(bin);
  }

  function hex(bytes, sep) {
    var parts = [];
    for (var i = 0; i < bytes.length; i++) parts.push((bytes[i] < 16 ? "0" : "") + bytes[i].toString(16).toUpperCase());
    return parts.join(sep);
  }

  // DER: one TLV at pos.
  function node(b, pos) {
    if (pos + 2 > b.length) throw new Error("The certificate is truncated.");
    var tag = b[pos];
    var len = b[pos + 1];
    var hdr = 2;
    if (len & 0x80) {
      var n = len & 0x7f;
      if (n < 1 || n > 4 || pos + 2 + n > b.length) throw new Error("The certificate has a bad length.");
      len = 0;
      for (var i = 0; i < n; i++) len = len * 256 + b[pos + 2 + i];
      hdr += n;
    }
    var start = pos + hdr;
    var end = start + len;
    if (end > b.length) throw new Error("The certificate is truncated.");
    return { tag: tag, start: start, end: end };
  }

  function children(b, n) {
    var out = [];
    var p = n.start;
    while (p < n.end) {
      var c = node(b, p);
      out.push(c);
      p = c.end;
    }
    return out;
  }

  function oid(b, n) {
    var parts = [];
    var v = 0;
    for (var i = n.start; i < n.end; i++) {
      v = v * 128 + (b[i] & 0x7f);
      if ((b[i] & 0x80) === 0) {
        if (parts.length === 0) {
          var first = v < 80 ? Math.floor(v / 40) : 2;
          parts.push(first, v - first * 40);
        } else {
          parts.push(v);
        }
        v = 0;
      }
    }
    return parts.join(".");
  }

  function text(b, n) {
    var bytes = b.subarray(n.start, n.end);
    if (n.tag === 0x1e) {
      var s = "";
      for (var i = 0; i + 1 < bytes.length; i += 2) s += String.fromCharCode(bytes[i] * 256 + bytes[i + 1]);
      return s;
    }
    return new TextDecoder("utf-8").decode(bytes);
  }

  function time(b, n) {
    var s = text(b, n);
    var y, rest;
    if (n.tag === 0x17) {
      y = Number(s.slice(0, 2));
      y = y < 50 ? 2000 + y : 1900 + y;
      rest = s.slice(2);
    } else {
      y = Number(s.slice(0, 4));
      rest = s.slice(4);
    }
    return y + "-" + rest.slice(0, 2) + "-" + rest.slice(2, 4) + " " + rest.slice(4, 6) + ":" + rest.slice(6, 8) + ":" + rest.slice(8, 10) + " UTC";
  }

  function name(b, n) {
    var parts = [];
    children(b, n).forEach(function (set) {
      children(b, set).forEach(function (atv) {
        var kv = children(b, atv);
        var key = oid(b, kv[0]);
        parts.push((NAMES[key] || key) + "=" + text(b, kv[1]));
      });
    });
    return parts.join(", ");
  }

  function commonName(b, n) {
    var cn = "";
    children(b, n).forEach(function (set) {
      children(b, set).forEach(function (atv) {
        var kv = children(b, atv);
        if (oid(b, kv[0]) === "2.5.4.3") cn = text(b, kv[1]);
      });
    });
    return cn;
  }

  function isCA(b, tbsFields) {
    for (var i = 0; i < tbsFields.length; i++) {
      if (tbsFields[i].tag !== 0xa3) continue;
      var exts = children(b, children(b, tbsFields[i])[0]);
      for (var j = 0; j < exts.length; j++) {
        var e = children(b, exts[j]);
        if (oid(b, e[0]) !== "2.5.29.19") continue;
        var value = e[e.length - 1];
        var bc = node(b, value.start);
        var inner = children(b, bc);
        return inner.length > 0 && inner[0].tag === 0x01 && b[inner[0].start] !== 0;
      }
    }
    return false;
  }

  function parseCert(der) {
    var cert = node(der, 0);
    if (cert.tag !== 0x30 || cert.end !== der.length) throw new Error("The data is not one DER certificate.");
    var top = children(der, cert);
    if (top.length !== 3 || top[0].tag !== 0x30) throw new Error("The data is not an X.509 certificate.");
    var f = children(der, top[0]);
    var i = f[0].tag === 0xa0 ? 1 : 0;
    var validity = children(der, f[i + 3]);
    return {
      subject: name(der, f[i + 4]),
      commonName: commonName(der, f[i + 4]),
      issuer: name(der, f[i + 2]),
      notBefore: time(der, validity[0]),
      notAfter: time(der, validity[1]),
      ca: isCA(der, f)
    };
  }

  function wrap(s, width) {
    var lines = [];
    for (var i = 0; i < s.length; i += width) lines.push(s.slice(i, i + width));
    return lines.join("\n");
  }

  function pem(der) {
    return "-----BEGIN CERTIFICATE-----\n" + wrap(toB64(der), 64) + "\n-----END CERTIFICATE-----\n";
  }

  function xmlEscape(s) {
    return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
  }

  // A UUID from 16 bytes of the fingerprint, so that a new download of the same certificate replaces the profile.
  function uuid(fp, offset) {
    var b = Array.prototype.slice.call(fp, offset, offset + 16);
    b[6] = (b[6] & 0x0f) | 0x40;
    b[8] = (b[8] & 0x3f) | 0x80;
    var h = hex(b, "");
    return h.slice(0, 8) + "-" + h.slice(8, 12) + "-" + h.slice(12, 16) + "-" + h.slice(16, 20) + "-" + h.slice(20, 32);
  }

  function mobileconfig(der, fp, cn) {
    var label = xmlEscape(cn || "care-pi");
    var id = "dev.rithviknishad.care-pi.cert." + hex(fp.subarray(0, 8), "").toLowerCase();
    return '<?xml version="1.0" encoding="UTF-8"?>\n' +
      '<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">\n' +
      '<plist version="1.0">\n<dict>\n' +
      "  <key>PayloadContent</key>\n  <array>\n    <dict>\n" +
      "      <key>PayloadCertificateFileName</key>\n      <string>care-pi-root.cer</string>\n" +
      "      <key>PayloadContent</key>\n      <data>\n" + wrap(toB64(der), 64) + "\n      </data>\n" +
      "      <key>PayloadDescription</key>\n      <string>Adds the care-pi root certificate " + label + ".</string>\n" +
      "      <key>PayloadDisplayName</key>\n      <string>" + label + "</string>\n" +
      "      <key>PayloadIdentifier</key>\n      <string>" + id + ".root</string>\n" +
      "      <key>PayloadType</key>\n      <string>com.apple.security.root</string>\n" +
      "      <key>PayloadUUID</key>\n      <string>" + uuid(fp, 0) + "</string>\n" +
      "      <key>PayloadVersion</key>\n      <integer>1</integer>\n" +
      "    </dict>\n  </array>\n" +
      "  <key>PayloadDescription</key>\n  <string>Trust the certificate of your care-pi device. Fingerprint (SHA-256): " + hex(fp, ":") + "</string>\n" +
      "  <key>PayloadDisplayName</key>\n  <string>care-pi: " + label + "</string>\n" +
      "  <key>PayloadIdentifier</key>\n  <string>" + id + "</string>\n" +
      "  <key>PayloadRemovalDisallowed</key>\n  <false/>\n" +
      "  <key>PayloadType</key>\n  <string>Configuration</string>\n" +
      "  <key>PayloadUUID</key>\n  <string>" + uuid(fp, 16) + "</string>\n" +
      "  <key>PayloadVersion</key>\n  <integer>1</integer>\n" +
      "</dict>\n</plist>\n";
  }

  function el(id) {
    return document.getElementById(id);
  }

  function setText(id, value) {
    var e = el(id);
    if (e) e.textContent = value;
  }

  function download(filename, mime, content) {
    var blob = new Blob([content], { type: mime });
    var url = URL.createObjectURL(blob);
    var a = document.createElement("a");
    a.href = url;
    a.download = filename;
    a.rel = "noopener";
    document.body.appendChild(a);
    a.click();
    a.remove();
    setTimeout(function () { URL.revokeObjectURL(url); }, 60000);
  }

  function fail(message) {
    var s = el("status");
    if (s) {
      s.textContent = message;
      s.className = "warn";
    }
    var d = el("details");
    if (d) d.hidden = true;
  }

  function main() {
    var fragment = location.hash ? location.hash.slice(1) : "";
    if (fragment === "") {
      setText("status", "");
      var empty = el("empty");
      if (empty) empty.hidden = false;
      return Promise.resolve(null);
    }
    if (fragment.length > MAX_FRAGMENT) {
      fail("The link is too long to hold one certificate.");
      return Promise.resolve(null);
    }
    var der, info;
    try {
      der = decodeB64(fragment);
      info = parseCert(der);
    } catch (e) {
      fail("The link does not hold a valid certificate: " + e.message);
      return Promise.resolve(null);
    }
    return crypto.subtle.digest("SHA-256", der).then(function (digest) {
      var fp = new Uint8Array(digest);
      var short = hex(fp.subarray(0, 4), "").toLowerCase();
      setText("subject", info.subject);
      setText("issuer", info.issuer);
      setText("not-before", info.notBefore);
      setText("not-after", info.notAfter);
      setText("ca", info.ca ? "Yes. This is a certificate authority." : "No. This is not a certificate authority. Do not install it as a root.");
      setText("fingerprint", hex(fp, ":"));
      setText("status", "The certificate is on this device only. Compare the fingerprint before you install it.");
      var d = el("details");
      if (d) d.hidden = false;
      var buttons = [
        ["dl-crt", "care-pi-" + short + ".crt", "application/x-x509-ca-cert", function () { return der; }],
        ["dl-pem", "care-pi-" + short + ".pem", "application/x-pem-file", function () { return pem(der); }],
        ["dl-mobileconfig", "care-pi-" + short + ".mobileconfig", "application/x-apple-aspen-config", function () { return mobileconfig(der, fp, info.commonName); }]
      ];
      buttons.forEach(function (spec) {
        var b = el(spec[0]);
        if (!b) return;
        b.disabled = false;
        b.addEventListener("click", function () { download(spec[1], spec[2], spec[3]()); });
      });
      return { fingerprint: hex(fp, ":"), info: info };
    });
  }

  var hook = typeof globalThis !== "undefined" ? globalThis.__CARE_PI_CERT_TEST__ : undefined;
  if (hook) {
    hook.api = { decodeB64: decodeB64, parseCert: parseCert, pem: pem, mobileconfig: mobileconfig, hex: hex };
  }
  var ready = main();
  if (hook) hook.ready = ready;
  window.addEventListener("hashchange", function () { location.reload(); });
})();
`;
