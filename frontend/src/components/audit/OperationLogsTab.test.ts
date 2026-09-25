import { describe, it, expect } from 'vitest';
import { ACTION_CONFIG, STATUS_CONFIG } from './operationLogConfig';

describe('OperationLogsTab configs', () => {
  it('contains configurations for all supported audit actions', () => {
    const expectedActions = [
      'update_id3',
      'batch_update_id3',
      'auto_scrape',
      'filename_parse',
      'tidy_folder',
      'download',
      'upload_cover',
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
});
