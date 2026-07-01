const config = {
    presets: [
        ['@babel/preset-env', {
            targets: {
                chrome: 66,
                firefox: 60,
                edge: 42,
                safari: 12,
            },
            modules: false,
            debug: false,
        }],
        ['@babel/preset-react', {
            runtime: 'classic',
        }],
        ['@babel/preset-typescript'],
    ],
    plugins: [
        ['polyfill-corejs3', {
            method: 'usage-global',
        }],
    ],
};

module.exports = config;
