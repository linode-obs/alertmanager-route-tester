((root, create) => {
    const api = create();
    root.createLatestDraftPreview = api.createLatestDraftPreview;
    if (typeof module !== 'undefined' && module.exports) module.exports = api;
})(globalThis, () => ({
    createLatestDraftPreview() {
        let current = 0;
        return {
            invalidate() {
                current++;
            },
            async run(operation, onSuccess, onFailure) {
                const request = ++current;
                try {
                    const result = await operation();
                    if (request === current) onSuccess(result);
                } catch (error) {
                    if (request === current) onFailure(error);
                }
            },
        };
    },
}));
