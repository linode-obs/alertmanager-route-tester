AI-generated content prepared on Will's behalf.

# Issue #14 route-ladder design reference

This static preview records the selected route-ladder direction. It includes five layout options, light and dark themes, and simulated label, route-test, and share states. Its CSS and interactions are embedded in `route-ladder-preview.html`. It sends no requests to Alertmanager.

The preview opens on the tested route-ladder state. Use the controls to compare the other layouts, themes, and action states.

Use this as a visual guide for future frontend work:

- Keep the tested route result beside the alert inputs on wide screens.
- Give long route paths enough room to read each matcher and receiver.
- Use blue and gray for the interface, with green reserved for delivery status.
- Keep configuration details available without competing with the route path.
- Use clear delivery wording such as “DELIVERS”.

Light theme:

![Route ladder in the light theme](route-ladder-light.png)

Dark theme:

![Route ladder in the dark theme](route-ladder-dark.png)

Action states: `label-added.png`, `route-tested.png`, and `link-shared.png`.
