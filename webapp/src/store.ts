import manifest from 'manifest';
import type {Action} from 'redux';

export const OPEN_REQUEST_MODAL = `${manifest.id}_open_request_modal`;
export const CLOSE_REQUEST_MODAL = `${manifest.id}_close_request_modal`;

export const openRequestModal = (): Action => ({type: OPEN_REQUEST_MODAL});
export const closeRequestModal = (): Action => ({type: CLOSE_REQUEST_MODAL});

type PluginState = {
    modalOpen: boolean;
};

// GlobalState is a minimal shape of the parts of the Redux store this plugin reads. Plugin state is
// namespaced under "plugins-<pluginId>" and team state lives under entities.teams.
export type GlobalState = {
    entities: {
        teams: {
            currentTeamId: string;
        };
    };
    [pluginKey: string]: unknown;
};

export const isRequestModalOpen = (state: GlobalState): boolean => {
    const pluginState = state[`plugins-${manifest.id}`] as PluginState | undefined;
    return Boolean(pluginState?.modalOpen);
};

export const getCurrentTeamId = (state: GlobalState): string => state.entities?.teams?.currentTeamId ?? '';

const initialState: PluginState = {modalOpen: false};

export default function reducer(state: PluginState = initialState, action: Action): PluginState {
    switch (action.type) {
    case OPEN_REQUEST_MODAL:
        return {...state, modalOpen: true};
    case CLOSE_REQUEST_MODAL:
        return {...state, modalOpen: false};
    default:
        return state;
    }
}
