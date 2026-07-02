import manifest from 'manifest';
import React, {useEffect, useRef, useState} from 'react';

// debug logs when localStorage.PLUGIN_CHANNEL_REQUESTS_DEBUG === "true"
// so we can trace autocomplete behavior in production without a code
// change. Enable via DevTools:
//   localStorage.setItem('PLUGIN_CHANNEL_REQUESTS_DEBUG', 'true')
function debug(...args: unknown[]): void {
    try {
        if (typeof localStorage !== 'undefined' && localStorage.getItem('PLUGIN_CHANNEL_REQUESTS_DEBUG') === 'true') {
            // eslint-disable-next-line no-console
            console.debug('[member-picker]', ...args);
        }
    } catch {
        // localStorage blocked in some sandboxes; ignore.
    }
}

// MemberPicker replaces the "Comma-separated usernames" text field in
// the request modal with a real autocomplete-driven picker.
//
// UX:
//   - Type in the input -> debounced fetch to /api/v1/user_autocomplete
//   - Matching users show as a dropdown below the input
//   - Click a user (or Enter on a highlighted one) -> add to the pill list
//   - "×" on a pill removes them
//   - The picker exposes value as a comma-separated username string via
//     onChange, so the parent Modal doesn't have to know about pills
//
// Debounce is 200ms — snappy enough to feel live, sparse enough not to
// hammer the endpoint on every keystroke.

type User = {
    id: string;
    username: string;
    nickname?: string;
    first_name?: string;
    last_name?: string;
};

type Props = {
    value: string; // comma-separated usernames
    disabled?: boolean;
    onChange: (value: string) => void;
    placeholder?: string;

    // teamId, when set, scopes autocomplete to members of that team.
    // Passed as ?team_id=... to /api/v1/user_autocomplete so the
    // dropdown only surfaces people who could actually be added to a
    // channel in this team.
    teamId?: string;

    // usernamesToExclude is a set of usernames already selected in a
    // sibling picker (e.g. the Members picker excludes anyone already
    // in the Channel Admins picker, and vice versa) so no user shows
    // up twice.
    usernamesToExclude?: string[];
};

// Parse comma-separated usernames -> array of trimmed non-empty names.
function parseUsernames(v: string): string[] {
    return v.
        split(',').
        map((s) => s.trim().replace(/^@/, '')).
        filter((s) => s.length > 0);
}

function displayName(u: User): string {
    const first = u.first_name ?? '';
    const last = u.last_name ?? '';
    const full = `${first} ${last}`.trim();
    if (full) {
        return `${u.username}  —  ${full}`;
    }
    if (u.nickname) {
        return `${u.username}  —  ${u.nickname}`;
    }
    return u.username;
}

export const MemberPicker: React.FC<Props> = ({value, disabled, onChange, placeholder, teamId, usernamesToExclude}) => {
    const [selected, setSelected] = useState<string[]>(() => parseUsernames(value));
    const [query, setQuery] = useState('');
    const [candidates, setCandidates] = useState<User[]>([]);
    const [showDropdown, setShowDropdown] = useState(false);
    const [highlightIndex, setHighlightIndex] = useState(0);
    const [dropdownRect, setDropdownRect] = useState<{top: number; left: number; width: number} | null>(null);
    const containerRef = useRef<HTMLDivElement>(null);

    // Track the input container's screen position so the dropdown can
    // render via position:fixed OUTSIDE the parent modal — the modal
    // has overflow:auto which would otherwise clip the dropdown when
    // this picker is near the bottom (like "Channel Admins to add").
    useEffect(() => {
        if (!showDropdown) {
            return undefined;
        }
        const updateRect = () => {
            const el = containerRef.current;
            if (!el) {
                return;
            }
            const r = el.getBoundingClientRect();
            setDropdownRect({top: r.bottom + 2, left: r.left, width: r.width});
        };
        updateRect();
        window.addEventListener('scroll', updateRect, true);
        window.addEventListener('resize', updateRect);
        return () => {
            window.removeEventListener('scroll', updateRect, true);
            window.removeEventListener('resize', updateRect);
        };
    }, [showDropdown, candidates.length]);

    // Re-sync when parent value changes externally.
    useEffect(() => {
        setSelected(parseUsernames(value));
    }, [value]);

    // Debounced autocomplete fetch. Team-scoped via ?team_id when the
    // parent passed teamId. Strips leading @ from the query before
    // sending — MM usernames don't contain @, so "@cfi" would never
    // match a real username; users expect to be able to type @-prefixed
    // like every other mention affordance.
    useEffect(() => {
        // Strip any leading @ AND trim. If nothing meaningful left,
        // clear the dropdown.
        const q = query.trim().replace(/^@+/, '').trim();
        if (!q) {
            setCandidates([]);
            return undefined;
        }
        const timer = window.setTimeout(async () => {
            try {
                const params = new URLSearchParams({q});
                if (teamId) {
                    params.set('team_id', teamId);
                }
                const url = `/plugins/${manifest.id}/api/v1/user_autocomplete?${params.toString()}`;
                debug('fetching', {url, q, teamId, selectedCount: selected.length});
                const response = await fetch(url, {
                    credentials: 'same-origin',
                    headers: {'X-Requested-With': 'XMLHttpRequest'},
                });
                if (!response.ok) {
                    debug('response not ok', {status: response.status});
                    setCandidates([]);
                    return;
                }
                const users: User[] = await response.json();
                debug('got users', {count: users.length, usernames: users.map((u) => u.username)});
                const excluded = new Set([...selected, ...(usernamesToExclude ?? [])]);
                const filtered = users.filter((u) => !excluded.has(u.username));
                debug('after exclude', {kept: filtered.length});
                setCandidates(filtered);
                setHighlightIndex(0);
            } catch (err) {
                debug('fetch threw', {err: String(err)});
                setCandidates([]);
            }
        }, 200);
        return () => window.clearTimeout(timer);
    }, [query, selected, teamId, usernamesToExclude]);

    // Close dropdown when clicking outside.
    useEffect(() => {
        const handler = (e: MouseEvent) => {
            if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
                setShowDropdown(false);
            }
        };
        document.addEventListener('mousedown', handler);
        return () => document.removeEventListener('mousedown', handler);
    }, []);

    const commit = (next: string[]) => {
        setSelected(next);
        onChange(next.join(', '));
    };

    const addUser = (username: string) => {
        if (!username || selected.includes(username)) {
            return;
        }
        commit([...selected, username]);
        setQuery('');
        setCandidates([]);
    };

    const removeUser = (username: string) => {
        commit(selected.filter((u) => u !== username));
    };

    const handleKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
        // With no visible dropdown, Enter still accepts the literal
        // typed name (useful for offline / unknown-user fallback).
        if (!showDropdown || candidates.length === 0) {
            if (e.key === 'Enter' && query.trim()) {
                e.preventDefault();
                addUser(query.trim().replace(/^@/, ''));
            }
            return;
        }
        if (e.key === 'ArrowDown') {
            e.preventDefault();
            setHighlightIndex((i) => Math.min(i + 1, candidates.length - 1));
        } else if (e.key === 'ArrowUp') {
            e.preventDefault();
            setHighlightIndex((i) => Math.max(i - 1, 0));
        } else if (e.key === 'Enter' || e.key === 'Tab') {
            // Tab completes the currently highlighted candidate — same
            // behavior as Enter. Prevents Tab from bouncing focus out
            // of the picker mid-selection.
            e.preventDefault();
            const u = candidates[highlightIndex];
            if (u) {
                addUser(u.username);
            }
        } else if (e.key === 'Escape') {
            setShowDropdown(false);
        } else if (e.key === 'Backspace' && !query && selected.length > 0) {
            // Backspace on empty input removes the last selected user.
            commit(selected.slice(0, -1));
        }
    };

    return (
        <div
            ref={containerRef}
            style={{position: 'relative'}}
        >
            <div
                style={{
                    display: 'flex',
                    flexWrap: 'wrap',
                    gap: 4,
                    padding: 6,
                    minHeight: 34,
                    border: '1px solid var(--center-channel-color-rgb, rgba(0,0,0,0.16))',
                    borderRadius: 4,
                    background: 'var(--center-channel-bg, #fff)',
                }}
                onClick={() => {
                    setShowDropdown(true);
                    (containerRef.current?.querySelector('input') as HTMLInputElement | null)?.focus();
                }}
            >
                {selected.map((username) => (
                    <span
                        key={username}
                        style={{
                            display: 'inline-flex',
                            alignItems: 'center',
                            gap: 4,
                            padding: '2px 8px',
                            borderRadius: 12,
                            background: 'rgba(28, 88, 217, 0.12)',
                            fontSize: 13,
                        }}
                    >
                        {'@' + username}
                        <button
                            type='button'
                            style={{
                                border: 'none',
                                background: 'transparent',
                                cursor: 'pointer',
                                padding: 0,
                                fontSize: 14,
                                lineHeight: 1,
                            }}
                            disabled={disabled}
                            onClick={(e) => {
                                e.stopPropagation();
                                removeUser(username);
                            }}
                        >
                            {'×'}
                        </button>
                    </span>
                ))}
                <input
                    style={{
                        flex: 1,
                        minWidth: 120,
                        border: 'none',
                        outline: 'none',
                        background: 'transparent',
                        fontSize: 14,
                        padding: '2px 4px',
                    }}
                    value={query}
                    placeholder={selected.length === 0 ? (placeholder ?? 'Type a name...') : ''}
                    disabled={disabled}
                    onChange={(e) => {
                        setQuery(e.target.value);
                        setShowDropdown(true);
                    }}
                    onFocus={() => setShowDropdown(true)}
                    onKeyDown={handleKeyDown}
                />
            </div>

            {showDropdown && candidates.length > 0 && dropdownRect ? (
                <div
                    style={{

                        // Fixed positioning + explicit top/left/width
                        // computed from the input container's DOMRect.
                        // Avoids clipping by any ancestor with
                        // overflow:auto (the modal is one such).
                        position: 'fixed',
                        top: dropdownRect.top,
                        left: dropdownRect.left,
                        width: dropdownRect.width,
                        background: 'var(--center-channel-bg, #fff)',
                        color: 'var(--center-channel-color, #3d3c40)',
                        border: '1px solid rgba(0, 0, 0, 0.12)',
                        borderRadius: 4,
                        boxShadow: '0 4px 12px rgba(0, 0, 0, 0.24)',
                        maxHeight: 240,
                        overflowY: 'auto',
                        zIndex: 2000,
                    }}
                >
                    {candidates.map((u, i) => (
                        <div
                            key={u.id}
                            style={{
                                padding: '6px 10px',
                                cursor: 'pointer',
                                background: i === highlightIndex ? 'rgba(28, 88, 217, 0.08)' : 'transparent',
                                fontSize: 14,
                            }}
                            onMouseEnter={() => setHighlightIndex(i)}
                            onMouseDown={(e) => {
                                // mousedown fires before blur so we can select without the outside-click closer eating it.
                                e.preventDefault();
                                addUser(u.username);
                            }}
                        >
                            {displayName(u)}
                        </div>
                    ))}
                </div>
            ) : null}
        </div>
    );
};
