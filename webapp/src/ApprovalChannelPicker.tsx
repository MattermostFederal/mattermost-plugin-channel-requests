import manifest from 'manifest';
import React, {useEffect, useRef, useState} from 'react';

// ApprovalChannelPicker: TWO coordinated custom admin-console settings
// that store the IDs (never names) for ApprovalTeam +
// ApprovalChannel. The team picker is a dropdown of teams the current
// admin is a member of. The channel picker is a dropdown of channels
// in whichever team is currently selected — updates reactively when
// the team changes.
//
// Because registerAdminConsoleCustomSetting maps one component per
// setting key, we export TWO components — TeamPicker (registered for
// key "ApprovalTeam") and ChannelPicker (registered for
// "ApprovalChannel") — and coordinate them through the `currentState`
// prop MM passes to each component so the channel picker sees the
// team selection immediately without a page reload.

type Team = {
    id: string;
    name: string;
    display_name: string;
};

type Channel = {
    id: string;
    name: string;
    display_name: string;
    type: string;
};

// MM plugin custom-setting props. `config` is the last-saved config;
// `currentState` reflects unsaved pending changes so a sibling picker
// can see the tentative value the admin just clicked.
type Props = {
    id: string;
    label?: string;
    value: string;
    disabled?: boolean;
    onChange: (id: string, value: string) => void;
    setSaveNeeded?: () => void;
    config?: Record<string, unknown>;
    currentState?: Record<string, unknown>;
};

// Read the current tentative value of a sibling plugin setting. MM
// nests plugin config under PluginSettings.Plugins.<plugin-id> — we
// try both currentState (unsaved) and config (saved), preferring the
// former. Returns "" if the key isn't found.
function readSiblingSetting(props: Props, key: string): string {
    const pluginID = manifest.id;
    const readFrom = (source?: Record<string, unknown>): string | undefined => {
        if (!source) {
            return undefined;
        }
        const plugins = (source.PluginSettings as {Plugins?: Record<string, Record<string, unknown>>} | undefined)?.Plugins;
        const pluginCfg = plugins?.[pluginID];
        if (!pluginCfg) {
            return undefined;
        }

        // Config keys are case-insensitively matched by MM; try both.
        const v = pluginCfg[key] ?? pluginCfg[key.toLowerCase()];
        return typeof v === 'string' ? v : undefined;
    };
    return readFrom(props.currentState) ?? readFrom(props.config) ?? '';
}

// Small fetch helpers — pull the team + channel lists from the plugin's
// own server endpoints (which respect the admin's per-team visibility).
async function fetchTeams(): Promise<Team[]> {
    try {
        const response = await fetch(`/plugins/${manifest.id}/api/v1/teams`, {
            credentials: 'same-origin',
            headers: {'X-Requested-With': 'XMLHttpRequest'},
        });
        if (!response.ok) {
            return [];
        }
        return await response.json();
    } catch {
        return [];
    }
}

async function fetchChannels(teamID: string): Promise<Channel[]> {
    if (!teamID) {
        return [];
    }
    try {
        const response = await fetch(`/plugins/${manifest.id}/api/v1/channels?team_id=${encodeURIComponent(teamID)}`, {
            credentials: 'same-origin',
            headers: {'X-Requested-With': 'XMLHttpRequest'},
        });
        if (!response.ok) {
            return [];
        }
        return await response.json();
    } catch {
        return [];
    }
}

// TeamPicker: dropdown of teams. Writes the team ID to ApprovalTeam. IDs,
// not slugs: a renamed team's slug can be re-created by anyone allowed to
// create teams, which would hand them the approval queue.
export const TeamPicker: React.FC<Props> = (props) => {
    const {id, value, disabled, onChange, setSaveNeeded} = props;
    const [teams, setTeams] = useState<Team[]>([]);
    const [loading, setLoading] = useState(true);

    useEffect(() => {
        fetchTeams().then((list) => {
            setTeams(list);
            setLoading(false);
        });
    }, []);

    return (
        <div>
            <select
                className='form-control'
                value={value ?? ''}
                disabled={disabled ?? loading}
                onChange={(e) => {
                    onChange(id, e.target.value);
                    setSaveNeeded?.();
                }}
            >
                <option value=''>{loading ? '-- loading teams --' : '-- pick a team --'}</option>
                {teams.map((t) => (
                    <option
                        key={t.id}
                        value={t.id}
                    >
                        {`${t.display_name || t.name}  (/${t.name})`}
                    </option>
                ))}
            </select>
            <small style={{opacity: 0.6}}>{'Pick the team that contains your approval channel. The channel dropdown below filters to this team.'}</small>
        </div>
    );
};

// ChannelPicker: dropdown of channels in the currently-selected team.
// Writes the channel ID to ApprovalChannel. Reads sibling ApprovalTeam (a
// team ID) via currentState so it updates the moment the admin picks a
// team, without a page reload.
export const ChannelPicker: React.FC<Props> = (props) => {
    const {id, value, disabled, onChange, setSaveNeeded} = props;
    const teamID = readSiblingSetting(props, 'ApprovalTeam');

    const [channels, setChannels] = useState<Channel[]>([]);
    const [loading, setLoading] = useState(false);

    // Read the latest value/onChange from the effect without making them
    // effect dependencies (which would re-run the fetch on every keystroke
    // elsewhere in the console). prevTeamRef distinguishes a real team
    // change from the initial mount.
    const valueRef = useRef(value);
    valueRef.current = value;
    const onChangeRef = useRef(onChange);
    onChangeRef.current = onChange;
    const prevTeamRef = useRef<string | null>(null);

    // Fetch the selected team's channels.
    useEffect(() => {
        // Guard against a slower earlier fetch resolving after a newer one
        // when the admin switches teams rapidly — a stale response must not
        // overwrite the current team's channels.
        let cancelled = false;

        const isTeamChange = prevTeamRef.current !== null && prevTeamRef.current !== teamID;
        prevTeamRef.current = teamID;

        // When the admin switches teams, clear any previously-selected
        // channel so a channel from the OLD team can't be saved against the
        // new one. Only on a genuine change — never clobber the saved
        // value on initial mount.
        const clearStaleChannel = () => {
            if (isTeamChange && valueRef.current) {
                onChangeRef.current(id, '');
                setSaveNeeded?.();
            }
        };

        if (!teamID) {
            setChannels([]);
            clearStaleChannel();
            return undefined;
        }
        setLoading(true);
        fetchChannels(teamID).then((list) => {
            if (cancelled) {
                return;
            }
            setChannels(list);
            setLoading(false);

            // Clear the selection if the team changed and the old channel
            // isn't one of the new team's channels.
            if (isTeamChange && valueRef.current && !list.some((c) => c.id === valueRef.current)) {
                onChangeRef.current(id, '');
                setSaveNeeded?.();
            }
        });
        return () => {
            cancelled = true;
        };
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [teamID]);

    return (
        <div>
            <select
                className='form-control'
                value={value ?? ''}
                disabled={disabled ?? (loading || !teamID)}
                onChange={(e) => {
                    onChange(id, e.target.value);
                    setSaveNeeded?.();
                }}
            >
                <option value=''>
                    {(() => {
                        if (!teamID) {
                            return '-- pick a team first --';
                        }
                        if (loading) {
                            return '-- loading channels --';
                        }
                        if (channels.length === 0) {
                            return '-- no channels visible --';
                        }
                        return '-- pick a channel --';
                    })()}
                </option>
                {channels.map((c) => (
                    <option
                        key={c.id}
                        value={c.id}
                    >
                        {`${c.type === 'P' ? '🔒 ' : ''}${c.display_name || c.name}  (/${c.name})`}
                    </option>
                ))}
            </select>
            <small style={{opacity: 0.6}}>
                {'The channel where new channel requests get posted for approval. '}
                {teamID ? '' : 'Pick a team above first.'}
            </small>
        </div>
    );
};
