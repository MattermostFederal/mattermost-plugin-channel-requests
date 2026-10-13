import manifest from 'manifest';
import React from 'react';
import {Provider} from 'react-redux';
import {combineReducers, createStore} from 'redux';

import {RequestChannelAdminModal} from './RequestChannelAdminModal';
import pluginReducer from './store';

// ChannelAdminModalHarness builds a self-contained Redux store in the browser
// (Playwright CT can't pass a store as a prop) and renders the modal. `open`
// seeds adminModalChannelId, which is what gates the modal open. Lives in its
// own module because CT can only mount components imported from non-test files
// ("stories").
export const ChannelAdminModalHarness = ({open}: {open: boolean}) => {
    const store = React.useMemo(() => createStore(
        combineReducers({
            [`plugins-${manifest.id}`]: pluginReducer as any,
            entities: (s = {}) => s,
        }),
        {
            [`plugins-${manifest.id}`]: {modalOpen: false, teamModalOpen: false, adminModalChannelId: open ? 'chan1' : null},
            entities: {
                teams: {currentTeamId: 'team1'},
                channels: {channels: {chan1: {id: 'chan1', display_name: 'Marketing'}}},
            },
        } as any,
    ), []);
    return (
        <Provider store={store}>
            <RequestChannelAdminModal/>
        </Provider>
    );
};
