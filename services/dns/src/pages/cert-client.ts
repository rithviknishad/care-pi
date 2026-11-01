// The script of GET /cert. It is plain browser JavaScript (ES2017) in a string, so the served bytes are
// exactly this text. test/node/cert-client.test.ts runs this script in Node with a small DOM stub.
//
// The script reads the certificate from location.hash, computes its SHA-256 fingerprint, shows the
// names and dates, and builds the .crt, .pem, and .mobileconfig files on this device. It makes no network request.
// It offers the files only for the root of a care-pi device: a CA with a critical Name Constraints extension that
// permits only names below .local and the care-pi zone, and only private IPv4 ranges (docs/connectivity.md 2.2).
// Anyone can put any certificate in the link, so the page must not help to install an unconstrained root.

export const CERT_JS = String.raw`"use strict";
(function () {
  var MAX_FRAGMENT = 16384;
  var NAMES = { "2.5.4.3": "CN", "2.5.4.6": "C", "2.5.4.7": "L", "2.5.4.8": "ST", "2.5.4.10": "O", "2.5.4.11": "OU" };
  // The DNS subtrees of a care-pi root, and the IPv4 ranges that a care-pi root can permit (RFC 1918 and the
  // shared address space 100.64.0.0/10 of CGNAT and Tailscale).
  var DOMAINS = [".local", ".care-pi.rithviknishad.dev"];
  var PRIVATE = [[[10, 0, 0, 0], 8], [[172, 16, 0, 0], 12], [[192, 168, 0, 0], 16], [[100, 64, 0, 0], 10]];

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

  // extension returns the extension with the OID id: its critical flag and the node of its value (inside the
  // OCTET STRING), or null.
  function extension(b, tbsFields, id) {
    for (var i = 0; i < tbsFields.length; i++) {
      if (tbsFields[i].tag !== 0xa3) continue;
      var exts = children(b, children(b, tbsFields[i])[0]);
      for (var j = 0; j < exts.length; j++) {
        var e = children(b, exts[j]);
        if (oid(b, e[0]) !== id) continue;
        var critical = e.length === 3 && e[1].tag === 0x01 && b[e[1].start] !== 0;
        return { critical: critical, value: node(b, e[e.length - 1].start) };
      }
    }
    return null;
  }

  function isCA(b, tbsFields) {
    var ext = extension(b, tbsFields, "2.5.29.19");
    if (!ext) return false;
    var inner = children(b, ext.value);
    return inner.length > 0 && inner[0].tag === 0x01 && b[inner[0].start] !== 0;
  }

  // prefixLength returns the length of a contiguous netmask, or -1.
  function prefixLength(mask) {
    var bits = 0;
    var ended = false;
    for (var i = 0; i < mask.length; i++) {
      for (var k = 7; k >= 0; k--) {
        if ((mask[i] >> k) & 1) {
          if (ended) return -1;
          bits++;
        } else {
          ended = true;
        }
      }
    }
    return bits;
  }

  // nameConstraints parses the Name Constraints extension (OID 2.5.29.30, RFC 5280 4.2.1.10): the permitted DNS
  // names, the permitted IP ranges ("a.b.c.d/n", or an IPv6 range), and the other permitted name types.
  // The excluded subtrees only narrow the permitted names, so the check does not need them.
  function nameConstraints(b, tbsFields) {
    var ext = extension(b, tbsFields, "2.5.29.30");
    if (!ext) return null;
    var out = { critical: ext.critical, dns: [], ips: [], ipv4: [], other: [] };
    children(b, ext.value).forEach(function (part) {
      if (part.tag !== 0xa0) return;
      children(b, part).forEach(function (subtree) {
        var base = children(b, subtree)[0];
        if (base.tag === 0x82) {
          out.dns.push(text(b, base));
        } else if (base.tag === 0x87) {
          var bytes = b.subarray(base.start, base.end);
          if (bytes.length === 8) {
            var bits = prefixLength(bytes.subarray(4, 8));
            var addr = [bytes[0], bytes[1], bytes[2], bytes[3]];
            out.ips.push(addr.join(".") + "/" + (bits < 0 ? "?" : bits));
            out.ipv4.push({ addr: addr, bits: bits });
          } else {
            var half = bytes.length / 2;
            var groups = [];
            for (var g = 0; g + 1 < half; g += 2) groups.push(((bytes[g] << 8) | bytes[g + 1]).toString(16));
            out.ips.push(groups.join(":") + "/" + prefixLength(bytes.subarray(half)));
            out.ipv4.push(null);
          }
        } else {
          out.other.push("0x" + base.tag.toString(16));
        }
      });
    });
    return out;
  }

  function inBlock(r, block) {
    if (r.bits < block[1]) return false;
    var a = ((r.addr[0] << 24) | (r.addr[1] << 16) | (r.addr[2] << 8) | r.addr[3]) >>> 0;
    var n = ((block[0][0] << 24) | (block[0][1] << 16) | (block[0][2] << 8) | block[0][3]) >>> 0;
    var mask = (0xffffffff << (32 - block[1])) >>> 0;
    return ((a & mask) >>> 0) === n;
  }

  function dnsAllowed(c) {
    var name = c.toLowerCase();
    for (var i = 0; i < DOMAINS.length; i++) {
      var d = DOMAINS[i];
      if (name === d) return true;
      if (name.length > d.length && name.slice(-d.length) === d) return true;
    }
    return false;
  }

  // problems returns why the certificate is not the root of a care-pi device ([] when it is one).
  function problems(info) {
    var out = [];
    if (!info.ca) out.push("It is not a certificate authority.");
    var nc = info.constraints;
    if (!nc) {
      out.push("It has no name constraints: it could sign a certificate for any website.");
      return out;
    }
    if (!nc.critical) out.push("Its name constraints are not marked critical.");
    if (nc.dns.length === 0) out.push("It permits each DNS name.");
    nc.dns.forEach(function (d) {
      if (!dnsAllowed(d)) out.push("It permits the DNS name \"" + (d === "" ? "(all)" : d) + "\", which is not below " + DOMAINS.join(" or ") + ".");
    });
    if (nc.ipv4.length === 0) out.push("It permits each IP address.");
    nc.ipv4.forEach(function (r, i) {
      if (!r) {
        out.push("It permits the IPv6 range " + nc.ips[i] + ".");
      } else if (r.bits < 0 || !PRIVATE.some(function (block) { return inBlock(r, block); })) {
        out.push("It permits the IP range " + nc.ips[i] + ", which is not a private IPv4 range.");
      }
    });
    return out;
  }

  function describeConstraints(nc) {
    if (!nc) return "None";
    var parts = [];
    parts.push("DNS: " + (nc.dns.length ? nc.dns.join(", ") : "(any)"));
    parts.push("IP: " + (nc.ips.length ? nc.ips.join(", ") : "(any)"));
    if (nc.other.length) parts.push("Other name types: " + nc.other.join(", "));
    parts.push(nc.critical ? "critical" : "not critical");
    return parts.join("; ");
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
      ca: isCA(der, f),
      constraints: nameConstraints(der, f)
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
      setText("constraints", describeConstraints(info.constraints));
      setText("fingerprint", hex(fp, ":"));
      var d = el("details");
      if (d) d.hidden = false;
      var bad = problems(info);
      if (bad.length > 0) {
        var s = el("status");
        if (s) {
          s.textContent = "This is not the certificate of a care-pi device. Do not install it. " + bad.join(" ");
          s.className = "warn";
        }
        return { fingerprint: hex(fp, ":"), info: info, problems: bad };
      }
      setText("status", "The certificate is on this device only. Compare the fingerprint before you install it.");
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
      return { fingerprint: hex(fp, ":"), info: info, problems: [] };
    });
  }

  var hook = typeof globalThis !== "undefined" ? globalThis.__CARE_PI_CERT_TEST__ : undefined;
  if (hook) {
    hook.api = { decodeB64: decodeB64, parseCert: parseCert, pem: pem, mobileconfig: mobileconfig, hex: hex, problems: problems };
  }
  var ready = main();
  if (hook) hook.ready = ready;
  window.addEventListener("hashchange", function () { location.reload(); });
})();
`;
