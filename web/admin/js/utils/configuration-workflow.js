export function approvalLabel(configuration) {
  const required = Number(configuration?.requiredApprovals || 0);
  if (required === 0) return 'Not required';
  return `${Number(configuration?.approvalCount || 0)} of ${required}`;
}

export function configurationActions(configuration) {
  const status = configuration?.status;
  const required = Number(configuration?.requiredApprovals || 0);
  const approved = Number(configuration?.approvalCount || 0);
  return {
    validate: status === 'draft',
    approve: status === 'validated' && required > 0 && approved < required,
    activate: status === 'validated' && approved >= required,
    rollback: status === 'superseded',
  };
}

export function configurationGuidance(configuration) {
  if (!configuration) return 'Select a snapshot to review its details and available actions.';
  const actions = configurationActions(configuration);
  if (actions.validate) return 'Validate this draft to check references, upstreams, access rules, and listener readiness.';
  if (actions.approve) return `Collect ${Number(configuration.requiredApprovals) - Number(configuration.approvalCount || 0)} more independent approval${Number(configuration.requiredApprovals) - Number(configuration.approvalCount || 0) === 1 ? '' : 's'} before activation.`;
  if (actions.activate) return 'This snapshot is ready. Activate it to publish the configuration to gateway replicas.';
  if (configuration.status === 'active') return 'This is the live snapshot currently served by the gateway fleet.';
  if (actions.rollback) return 'This historical snapshot can be restored as a new active configuration.';
  return 'This immutable snapshot has no available lifecycle action.';
}
