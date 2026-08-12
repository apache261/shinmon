export const MODULE_HELP = Object.freeze({
  overview: {
    title: 'Overview help',
    introduction: 'Use Overview to confirm that the control plane, active configuration, and gateway replicas agree before making traffic changes.',
    steps: ['Review inventory and readiness cards.', 'Check recent audit activity for unexpected changes.', 'Open the relevant module from the navigation when a metric needs attention.'],
    tips: ['Overview is read-only.', 'A ready replica should report the active configuration version.'],
  },
  services: {
    title: 'Services help',
    introduction: 'Services describe an API, its deployable versions, and the literal-IP upstreams that receive gateway traffic.',
    steps: ['Create a service and select it in the Services grid.', 'Add and select a version.', 'Add one or more upstream IP addresses and ports.', 'Use the contextual Edit actions to revise service, version, or upstream settings.', 'Create or activate a configuration after changes are ready.'],
    tips: ['Service names are lowercase identifiers.', 'Version names can use any 1–128 characters as long as they contain no whitespace.', 'DNS names are intentionally rejected; upstream addresses must be literal IPs.', 'HTTPS upstream certificates must contain the literal IP address in their subject alternative names.'],
  },
  ports: {
    title: 'Ports & listeners help',
    introduction: 'Listeners connect one API version and its access rule to a port in the environment pool.',
    steps: ['Add an access rule by choosing an API and what clients may do, or let Allocate listener guide you when none exist.', 'Choose Allocate listener and select an API, available port, required access, request types, and accepted content.', 'Open Configurations, then create, validate, and activate a snapshot to publish the listener.', 'Use Connection details to confirm publication, copy the client address, and inspect its target IP addresses.', 'Use Drain before Disable when removing live traffic.'],
    tips: ['Shinmon creates the technical permission name automatically.', 'The administrator-defined unprotected route regex bypasses API keys only for GET and HEAD; operations such as POST remain protected.', 'Anchor the regex and avoid matching ordinary API response paths.', 'The client address uses the hostname where you opened the dashboard plus the allocated listener port.', 'Target IP addresses are for operators; distribute only the client address and an authorized API key.', 'Leaving Preferred port empty lets Shinmon allocate one.', 'Policy changes require a new active configuration before gateways load them.'],
  },
  consumers: {
    title: 'Consumers & keys help',
    introduction: 'Consumers identify API clients. Access rules control what they may use, while issued keys are displayed only once.',
    steps: ['Add access rules by choosing the API and a plain-language access level.', 'Add an API client and choose its allowed access.', 'Select the client and create a key; the key automatically inherits the client access.', 'Copy the key immediately; rotate or revoke it when required.'],
    tips: ['Use Edit to change a client or access-rule notes.', 'Clients without key history and unused access rules can be removed.', 'Keys with security history are revoked rather than deleted.', 'Revocation is immediate across coordinated gateway replicas.'],
  },
  configurations: {
    title: 'Configurations help',
    introduction: 'Configurations are complete, immutable snapshots. They let operators review and publish management changes to every gateway replica as one atomic update.',
    steps: ['Finish service, listener, access, or traffic-policy changes in their modules.', 'Choose Create snapshot to capture the current management state.', 'Select the draft and validate it; validation checks references and enabled upstreams.', 'Collect independent approvals if the selected snapshot shows an approval requirement.', 'Activate the ready snapshot, or select a superseded version to restore it as a new live snapshot.'],
    tips: ['The inspector shows only actions valid for the selected snapshot.', 'Creating a snapshot does not affect live traffic.', 'A rollback never edits history; it creates a new active version from the selected snapshot.'],
  },
  gateways: {
    title: 'Gateway health help',
    introduction: 'Gateway health shows replica readiness, heartbeat freshness, and the configuration version loaded by each instance.',
    steps: ['Confirm every expected replica appears.', 'Compare loaded configuration versions with the active configuration.', 'Investigate replicas that are not ready or have stale last-seen times.'],
    tips: ['This module is read-only.', 'A replica can remain available during control-plane database interruptions using its last valid snapshot.'],
  },
  audit: {
    title: 'Audit help',
    introduction: 'Audit provides the immutable history of administrative changes and the actor responsible for each operation.',
    steps: ['Filter by action, resource, actor, or identifier.', 'Use the time column to establish change order.', 'Correlate an event with the affected management module.'],
    tips: ['Audit records are read-only.', 'The dashboard actor is the administrator identity supplied at login.'],
  },
});
