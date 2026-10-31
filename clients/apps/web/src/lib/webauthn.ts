/**
 * WebAuthn's encoding both ways: the server sends base64url text and
 * `navigator.credentials` wants `ArrayBuffer`s. Getting it wrong fails with no
 * useful browser error.
 */

import { base64urlToBuffer, bufferToBase64url } from './base64url'

/** False rather than a throw when there is no DOM, as under some tests. */
export function isSecureContext(): boolean {
  return typeof window !== 'undefined' && window.isSecureContext
}

interface CredentialDescriptorJSON {
  id: string
  type: 'public-key'
  transports?: AuthenticatorTransport[]
}

/** `POST /auth/passkeys/register/options`'s `options` field. */
export interface PasskeyCreationOptionsJSON {
  publicKey: Omit<PublicKeyCredentialCreationOptions, 'challenge' | 'user' | 'excludeCredentials'> & {
    challenge: string
    user: Omit<PublicKeyCredentialUserEntity, 'id'> & { id: string }
    excludeCredentials?: CredentialDescriptorJSON[]
  }
}

/** `POST /auth/passkeys/authenticate/options`'s `options` field. */
export interface PasskeyRequestOptionsJSON {
  publicKey: Omit<PublicKeyCredentialRequestOptions, 'challenge' | 'allowCredentials'> & {
    challenge: string
    allowCredentials?: CredentialDescriptorJSON[]
  }
}

/** Turns registration options from the server into what `credentials.create` takes. */
export function creationOptionsFromServer(json: PasskeyCreationOptionsJSON): CredentialCreationOptions {
  const { publicKey } = json
  return {
    publicKey: {
      ...publicKey,
      challenge: base64urlToBuffer(publicKey.challenge),
      user: { ...publicKey.user, id: base64urlToBuffer(publicKey.user.id) },
      excludeCredentials: (publicKey.excludeCredentials ?? []).map((cred) => ({
        ...cred,
        id: base64urlToBuffer(cred.id),
      })),
    },
  }
}

/** Turns authentication options from the server into what `credentials.get` takes. */
export function requestOptionsFromServer(json: PasskeyRequestOptionsJSON): CredentialRequestOptions {
  const { publicKey } = json
  return {
    publicKey: {
      ...publicKey,
      challenge: base64urlToBuffer(publicKey.challenge),
      allowCredentials: (publicKey.allowCredentials ?? []).map((cred) => ({
        ...cred,
        id: base64urlToBuffer(cred.id),
      })),
    },
  }
}

function isAttestationResponse(
  response: AuthenticatorResponse,
): response is AuthenticatorAttestationResponse {
  return 'attestationObject' in response
}

function isAssertionResponse(
  response: AuthenticatorResponse,
): response is AuthenticatorAssertionResponse {
  return 'signature' in response
}

/** The JSON body `/auth/passkeys/register/verify` expects as `credential`. */
export function registrationCredentialToJSON(credential: PublicKeyCredential): unknown {
  const { response } = credential
  if (!isAttestationResponse(response)) {
    throw new Error('The browser returned a login response from a registration ceremony.')
  }
  return {
    id: credential.id,
    rawId: bufferToBase64url(credential.rawId),
    type: credential.type,
    response: {
      clientDataJSON: bufferToBase64url(response.clientDataJSON),
      attestationObject: bufferToBase64url(response.attestationObject),
    },
    clientExtensionResults: credential.getClientExtensionResults(),
  }
}

/** The JSON body `/auth/passkeys/authenticate/verify` expects as `credential`. */
export function authenticationCredentialToJSON(credential: PublicKeyCredential): unknown {
  const { response } = credential
  if (!isAssertionResponse(response)) {
    throw new Error('The browser returned a registration response from a login ceremony.')
  }
  return {
    id: credential.id,
    rawId: bufferToBase64url(credential.rawId),
    type: credential.type,
    response: {
      clientDataJSON: bufferToBase64url(response.clientDataJSON),
      authenticatorData: bufferToBase64url(response.authenticatorData),
      signature: bufferToBase64url(response.signature),
      userHandle: response.userHandle ? bufferToBase64url(response.userHandle) : undefined,
    },
    clientExtensionResults: credential.getClientExtensionResults(),
  }
}
