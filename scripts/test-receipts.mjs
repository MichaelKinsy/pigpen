// Throwaway keys and receipts signed the way pig signs them, for tests. Nothing here is a real key.
import { generateKeyPairSync, sign } from 'node:crypto';
import { PAYLOAD_TYPE, keyIdOf, pae } from './receipts.mjs';

export const repository = 'MichaelKinsy/pigpen';
export const pair = () => generateKeyPairSync('ed25519');
export const rawOf = (publicKey) => publicKey.export({ format: 'der', type: 'spki' }).subarray(-32);
export const pem = (publicKey) => publicKey.export({ format: 'pem', type: 'spki' });
export const digest = (c) => c.repeat(64);

export function payload(over = {}) {
  return { piglet: 'herdr', version: '0.1.0', pigVersion: '0.4.0+1.0.0', sourceRef: 'git:https://github.com/MichaelKinsy/pigpen.git@' + 'a'.repeat(40),
    github: { repository, tagPrefix: 'herdr/' }, signer: { keyId: '', publicKey: '' },
    binaries: {
      'linux/amd64': { url: 'pig-herdr-linux-amd64', sha256: digest('1'), size: 1234 },
      'windows/amd64': { url: 'pig-herdr-windows-amd64.exe', sha256: digest('2'), size: 2345 },
    }, ...over };
}

/** A receipt signed like pig does: DSSE, the signer's key id and public key inside the signed payload. */
export function receipt({ privateKey, publicKey }, body = payload(), { keyid } = {}) {
  const raw = rawOf(publicKey);
  const id = keyIdOf(raw);
  const signed = { ...body, signer: body.signer?.keyId ? body.signer : { keyId: id, publicKey: raw.toString('base64') } };
  const bytes = Buffer.from(JSON.stringify(signed));
  const sig = sign(null, pae(PAYLOAD_TYPE, bytes), privateKey);
  return JSON.stringify({ payloadType: PAYLOAD_TYPE, payload: bytes.toString('base64'), signatures: [{ keyid: keyid ?? id, sig: sig.toString('base64') }] }, null, 2) + '\n';
}
