/**
 * App entry point.
 *
 * S1 delivers the protocol foundation only (lib/*). Auth, sync and pages land
 * in S2-S5, so onLaunch is intentionally a placeholder: the contract baseline
 * below is the one thing that must be checked before any sync runs.
 */

import { CONTRACT_SHA256, CONTRACT_EPOCH } from './lib/contract'

App({
  onLaunch() {
    // Contract identity is asserted against the server's capabilities response
    // at sync time; these constants are the frozen baseline.
    console.info('[flowspace] contract', CONTRACT_EPOCH, CONTRACT_SHA256.slice(0, 12))
  },
})
