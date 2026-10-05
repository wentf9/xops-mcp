// Compact JWE (RFC 7516): RSA-OAEP-256 plus A256GCM. Use Web Crypto when
// available and a pinned, embedded implementation for ordinary HTTP origins.
// Every key, nonce and OAEP seed comes from the browser's secure random source.
const encoder = new TextEncoder();
function base64url(bytes) {
  let text = "";
  for (const byte of bytes) text += String.fromCharCode(byte);
  return btoa(text).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}
function decodeBase64url(value) {
  if (typeof value !== "string" || !/^[A-Za-z0-9_-]+$/.test(value))
    throw new Error("密码加密公钥不正确");
  return Uint8Array.from(atob(value.replace(/-/g, "+").replace(/_/g, "/")), char => char.charCodeAt(0));
}
function randomBytes(length) {
  if (typeof globalThis.crypto?.getRandomValues !== "function")
    throw new Error("浏览器不支持安全随机数，请更新浏览器");
  return crypto.getRandomValues(new Uint8Array(length));
}
export async function encryptRequest(apiRoot, action, data, token = "") {
  const headers = { Accept: "application/json" };
  if (action === "password" && token) headers.Authorization = "Bearer " + token;
  const response = await fetch(new URL("auth/challenge?action=" + action, apiRoot), {
    headers, credentials: "omit", cache: "no-store",
  });
  const parameters = await response.json();
  if (!response.ok) throw new Error(parameters.message || "无法获取密码加密参数");
  const jwk = parameters.publicKey;
  if (!jwk || jwk.kty !== "RSA" || jwk.alg !== "RSA-OAEP-256" || !jwk.kid)
    throw new Error("密码加密公钥不正确");
  const modulus = decodeBase64url(jwk.n), exponent = decodeBase64url(jwk.e);
  if (modulus.length < 256 || modulus.length > 512 || exponent.length > 4)
    throw new Error("密码加密公钥长度不正确");
  const rawKey = randomBytes(32), iv = randomBytes(12);
  const plaintext = encoder.encode(JSON.stringify({ challenge: parameters.challenge, data }));
  try {
    const protectedHeader = base64url(encoder.encode(JSON.stringify({
      alg: "RSA-OAEP-256", enc: "A256GCM", kid: jwk.kid,
    })));
    const aad = encoder.encode(protectedHeader);
    let wrapped, encrypted;
    if (globalThis.crypto?.subtle) {
      const publicKey = await crypto.subtle.importKey(
        "jwk", jwk, { name: "RSA-OAEP", hash: "SHA-256" }, false, ["encrypt"],
      );
      const aes = await crypto.subtle.importKey("raw", rawKey, "AES-GCM", false, ["encrypt"]);
      wrapped = new Uint8Array(await crypto.subtle.encrypt({ name: "RSA-OAEP" }, publicKey, rawKey));
      encrypted = new Uint8Array(await crypto.subtle.encrypt({
        name: "AES-GCM", iv, additionalData: aad, tagLength: 128,
      }, aes, plaintext));
    } else {
      const { RSA_OAEP, Sha256, AES_GCM } = await import("./vendor/asmcrypto.js");
      const seed = randomBytes(32);
      try { wrapped = new RSA_OAEP([modulus, exponent], new Sha256()).encrypt(rawKey, seed); }
      finally { seed.fill(0); }
      encrypted = AES_GCM.encrypt(plaintext, rawKey, iv, aad, 16);
    }
    return { ciphertext: [protectedHeader, base64url(wrapped), base64url(iv),
      base64url(encrypted.subarray(0, -16)), base64url(encrypted.subarray(-16))].join(".") };
  } finally {
    rawKey.fill(0);
    plaintext.fill(0);
  }
}
