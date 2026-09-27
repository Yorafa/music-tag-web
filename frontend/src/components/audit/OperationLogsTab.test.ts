import { describe, it, expect } from 'vitest';
import {
  ACTION_CONFIG,
  ACTION_FILTER_OPTIONS,
  STATUS_CONFIG,
} from './operationLogConfig';

describe('OperationLogsTab configs', () => {
  it('contains configurations for all supported audit actions', () => {
    const expectedActions = [
      'update_id3',
      'batch_update_id3',
      'auto_scrape',
      'filename_parse',
      'tidy_folder',
      'prune_empty_folders',
      'download',
      'upload_cover',
      'delete_files',
      'trash_purge',
    ];

    for (const action of expectedActions) {
      expect(ACTION_CONFIG[action]).toBeDefined();
      expect(ACTION_CONFIG[action].label).toBeTruthy();
      expect(ACTION_CONFIG[action].icon).toBeDefined();
      expect(ACTION_CONFIG[action].color).toContain('text-');
    }
  });

  it('contains configurations for all operation statuses', () => {
    const expectedStatuses = ['success', 'failed', 'partial', 'skipped'];

    for (const status of expectedStatuses) {
      expect(STATUS_CONFIG[status]).toBeDefined();
      expect(STATUS_CONFIG[status].label).toBeTruthy();
      expect(STATUS_CONFIG[status].icon).toBeDefined();
      expect(STATUS_CONFIG[status].color).toContain('text-');
    }
  });

  // The filter used to be a hand-written second copy of the action list,
  // which is how delete_files ended up with no label and no way to filter
  // on it. Derived options cannot drift from the labels they render.
  it('offers a filter option for every action, and nothing else', () => {
    expect(ACTION_FILTER_OPTIONS.map((o) => o.value).sort()).toEqual(
      Object.keys(ACTION_CONFIG).sort(),
    );
    for (const opt of ACTION_FILTER_OPTIONS) {
      expect(opt.label).toBe(ACTION_CONFIG[opt.value].label);
    }
  });

  it('tells an unrecoverable purge apart from a recoverable delete', () => {
    // Both end in "delete", and the difference is the one thing an
    // operator reading the log needs to know.
    expect(ACTION_CONFIG.trash_purge.label).not.toBe(ACTION_CONFIG.delete_files.label);
  });
});
