import manifest from 'manifest';
import React from 'react';
import {Provider} from 'react-redux';
import {combineReducers, createStore} from 'redux';

import {RequestChannelModal} from './RequestChannelModal';
import pluginReducer from './store';

// ChannelModalHarness builds a self-contained Redux store in the browser
// (Playwright CT can't pass a store as a prop) and renders the modal. `open`
// seeds the channel-modal state. Lives in its own module because CT can only
// mount components imported from non-test files ("stories"). The modal fetches
// its prefix list on open, so tests should mock /api/v1/prefixes.
export const ChannelModalHarness = ({open}: {open: boolean}) => {
    const store = React.useMemo(() => createStore(
        combineReducers({
            [`plugins-${manifest.id}`]: pluginReducer as any,
            entities: (s = {teams: {currentTeamId: 'team1'}}) => s,
        }),
        {
            [`plugins-${manifest.id}`]: {modalOpen: open, teamModalOpen: false, adminModalChannelId: null},
            entities: {teams: {currentTeamId: 'team1'}},
        } as any,
    ), []);
    return (
        <Provider store={store}>
            <RequestChannelModal/>
        </Provider>
    );
};
