import { api } from '../api/client';

// base64url helpers
function bufferToBase64url(buffer: ArrayBuffer): string {
  const bytes = new Uint8Array(buffer);
  let binary = '';
  for (const b of bytes) binary += String.fromCharCode(b);
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

function base64urlToBuffer(base64url: string): ArrayBuffer {
  const base64 = base64url.replace(/-/g, '+').replace(/_/g, '/');
  const binary = atob(base64);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
  return bytes.buffer;
}

export async function registerPasskey(name?: string): Promise<void> {
  const options = await api.webauthnRegisterBegin(name);

  // Convert base64url strings to ArrayBuffers for the browser API
  const publicKey = options.publicKey;
  publicKey.challenge = base64urlToBuffer(publicKey.challenge);
  publicKey.user.id = base64urlToBuffer(publicKey.user.id);
  if (publicKey.excludeCredentials) {
    for (const c of publicKey.excludeCredentials) {
      c.id = base64urlToBuffer(c.id);
    }
  }

  const credential = await navigator.credentials.create({ publicKey }) as PublicKeyCredential;
  if (!credential) throw new Error('Passkey creation cancelled');

  const response = credential.response as AuthenticatorAttestationResponse;
  await api.webauthnRegisterFinish({
    id: credential.id,
    rawId: bufferToBase64url(credential.rawId),
    type: credential.type,
    response: {
      attestationObject: bufferToBase64url(response.attestationObject),
      clientDataJSON: bufferToBase64url(response.clientDataJSON),
    },
  }, name);
}

export async function loginWithPasskey(username?: string): Promise<void> {
  const options = await api.webauthnLoginBegin(username);

  const publicKey = options.publicKey;
  publicKey.challenge = base64urlToBuffer(publicKey.challenge);
  if (publicKey.allowCredentials) {
    for (const c of publicKey.allowCredentials) {
      c.id = base64urlToBuffer(c.id);
    }
  }

  const credential = await navigator.credentials.get({ publicKey }) as PublicKeyCredential;
  if (!credential) throw new Error('Passkey authentication cancelled');

  const response = credential.response as AuthenticatorAssertionResponse;
  await api.webauthnLoginFinish({
    id: credential.id,
    rawId: bufferToBase64url(credential.rawId),
    type: credential.type,
    response: {
      authenticatorData: bufferToBase64url(response.authenticatorData),
      clientDataJSON: bufferToBase64url(response.clientDataJSON),
      signature: bufferToBase64url(response.signature),
      userHandle: response.userHandle ? bufferToBase64url(response.userHandle) : null,
    },
  });
}
