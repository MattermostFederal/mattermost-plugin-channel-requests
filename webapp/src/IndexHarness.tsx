import React, {useEffect, useState} from 'react';

/**
 * Harness for the plugin entry point.
 *
 * The entry point calls `window.registerPlugin(...)` at import time, so the
 * stub has to be in place before the module is pulled in: hence a dynamic
 * import. A Proxy registry records every method `initialize` calls. The
 * harness then runs the registered header actions and the menu item's
 * shouldRender, and serializes everything into a text node for the test.
 */

export type Recorded = {
    registeredId: string;
    called: string[];
    adminSettings: string[];
    rootComponents: string[];
    headerButtonText: string;
    headerDispatch: unknown;
    menuText: string;
    menuDispatch: unknown;
    shouldRender: Record<string, boolean>;
    errors: string[];
};

type Props = {

    // throwOn names a registry method that throws when called.
    throwOn?: string;

    // withoutAdminConsole leaves registerAdminConsoleCustomSetting undefined,
    // like an older Mattermost.
    withoutAdminConsole?: boolean;
};

type Fn = (...args: unknown[]) => unknown;

export const IndexHarness = ({throwOn, withoutAdminConsole}: Props) => {
    const [result, setResult] = useState('');

    useEffect(() => {
        const run = async () => {
            const calls: Array<{name: string; args: unknown[]}> = [];
            const errors: string[] = [];
            const dispatched: unknown[] = [];

            // eslint-disable-next-line no-console
            const originalError = console.error;
            // eslint-disable-next-line no-console
            console.error = (...args: unknown[]) => {
                errors.push(String(args[0]));
            };

            const registry = new Proxy({}, {
                get: (_target, prop: string) => {
                    if (withoutAdminConsole && prop === 'registerAdminConsoleCustomSetting') {
                        return undefined;
                    }
                    return (...args: unknown[]) => {
                        calls.push({name: prop, args});
                        if (prop === throwOn) {
                            throw new Error(`${prop} unavailable`);
                        }
                    };
                },
            });
            const store = {
                getState: () => ({entities: {teams: {currentTeamId: 't1'}, users: {currentUserId: 'me', profiles: {}}}}),
                dispatch: (action: unknown) => {
                    dispatched.push(action);
                    return action;
                },
                subscribe: () => () => undefined,
            };

            let plugin: {initialize: (r: unknown, s: unknown) => Promise<void>} | undefined;
            let registeredId = '';
            window.registerPlugin = (id: string, p: unknown) => {
                registeredId = id;
                plugin = p as typeof plugin;
            };
            await import('./index');
            await plugin!.initialize(registry, store);
            // eslint-disable-next-line no-console
            console.error = originalError;

            const find = (name: string) => calls.find((c) => c.name === name);
            const header = find('registerChannelHeaderButtonAction');
            const menu = find('registerChannelHeaderMenuAction');

            let headerDispatch: unknown = null;
            if (header) {
                (header.args[1] as Fn)();
                headerDispatch = dispatched.at(-1);
            }

            let menuDispatch: unknown = null;
            const shouldRender: Record<string, boolean> = {};
            if (menu) {
                (menu.args[1] as Fn)('chan1');
                menuDispatch = dispatched.at(-1);
                for (const type of ['O', 'P', 'D', 'G', '']) {
                    const state = {entities: {teams: {currentTeamId: 't1'}, channels: {currentChannelId: 'c', channels: {c: {type}}}}};
                    shouldRender[type || 'unknown'] = (menu.args[2] as Fn)(state) as boolean;
                }
            }

            const recorded: Recorded = {
                registeredId,
                called: calls.map((c) => c.name),
                adminSettings: calls.filter((c) => c.name === 'registerAdminConsoleCustomSetting').map((c) => String(c.args[0])),
                rootComponents: calls.filter((c) => c.name === 'registerRootComponent').map((c) => (c.args[0] as {name: string}).name),
                headerButtonText: header ? String(header.args[2]) : '',
                headerDispatch,
                menuText: menu ? String(menu.args[0]) : '',
                menuDispatch,
                shouldRender,
                errors,
            };
            setResult(JSON.stringify(recorded));
        };
        run().catch((err) => setResult(JSON.stringify({error: String(err)})));
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, []);

    return <pre data-testid='recorded'>{result}</pre>;
};
