// makeDebug returns a logger that emits console.debug only when
// localStorage.PLUGIN_CHANNEL_REQUESTS_DEBUG === 'true', so we can trace
// plugin behavior in production without a code change. Enable via DevTools:
//   localStorage.setItem('PLUGIN_CHANNEL_REQUESTS_DEBUG', 'true')
//
// The debug-key contract lives here so both the request modal's member
// picker and the sidebar channel-creation override share one switch.
const DEBUG_LS_KEY = 'PLUGIN_CHANNEL_REQUESTS_DEBUG';

export function makeDebug(prefix: string): (...args: unknown[]) => void {
    return (...args: unknown[]): void => {
        try {
            if (typeof localStorage !== 'undefined' && localStorage.getItem(DEBUG_LS_KEY) === 'true') {
                // eslint-disable-next-line no-console
                console.debug(`[${prefix}]`, ...args);
            }
        } catch {
            // localStorage may throw in some sandbox contexts; ignore.
        }
    };
}
