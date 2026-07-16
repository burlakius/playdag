/**
 * The playdag wire contract, restated in TypeScript. This file is the only part
 * of the adapter that the orchestrator constrains; everything else here is
 * Playwright's business. See docs/PROTOCOL.md for the specification.
 */

export const SCHEMA_VERSION = '1.0';

/** Environment variable carrying the encoded request. */
export const CONTEXT_ENV = 'PLAYDAG_CONTEXT';

/** Environment variable naming the file the response is written to. */
export const RESULT_ENV = 'PLAYDAG_RESULT';

export type Status = 'PASSED' | 'FAILED' | 'SKIPPED';

export interface PlaydagRequest {
  schema_version: string;
  test_id: string;
  script?: string;
  attempt: number;
  base_url?: string;
  requires?: string[];
  provides?: string[];
  context?: Record<string, string>;
}

export interface PlaydagArtifact {
  type: string;
  path: string;
}

export interface PlaydagResponse {
  schema_version: string;
  status: Status;
  provides?: Record<string, string>;
  error?: string;
  artifacts?: PlaydagArtifact[];
}

/**
 * Reads the request the orchestrator passed in. Returns null when the tests are
 * run directly by Playwright rather than through playdag, which is what lets the
 * same spec files be debugged with `npx playwright test`.
 */
export function readRequest(env: NodeJS.ProcessEnv = process.env): PlaydagRequest | null {
  const raw = env[CONTEXT_ENV];
  if (!raw) return null;

  let req: PlaydagRequest;
  try {
    req = JSON.parse(raw);
  } catch (e) {
    throw new Error(`playdag: ${CONTEXT_ENV} is not valid JSON: ${(e as Error).message}`);
  }
  if (req.schema_version !== SCHEMA_VERSION) {
    throw new Error(
      `playdag: unsupported schema_version ${JSON.stringify(req.schema_version)} (want ${SCHEMA_VERSION})`,
    );
  }
  return req;
}
