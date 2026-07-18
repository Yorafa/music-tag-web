import { describe, it, expect, beforeEach, vi } from 'vitest';
import { useYoutubeStore } from '@/store/useYoutubeStore';

// axios is mocked so ensureDownload can run without network. We also
// assert the call count to verify the idempotent contract.
vi.mock('axios', () => ({
  default: {
    post: vi.fn(async () => ({ data: { result: true, code: '200', data: [], message: 'success' } })),
  },
}));

import axios from 'axios';
const postMock = axios.post as unknown as ReturnType<typeof vi.fn>;

beforeEach(() => {
  postMock.mockClear();
  useYoutubeStore.setState({ downloaded: new Set(), inFlight: new Map() });
});

describe('useYoutubeStore', () => {
  it('ensureDownload POSTs on first call per video id', async () => {
    await useYoutubeStore.getState().ensureDownload('vid-1');
    expect(postMock).toHaveBeenCalledTimes(1);
    expect(postMock).toHaveBeenCalledWith('/api/download/', { source: 'youtube', video_id: 'vid-1' });
    expect(useYoutubeStore.getState().downloaded.has('vid-1')).toBe(true);
    expect(useYoutubeStore.getState().inFlight.has('vid-1')).toBe(false);
  });

  it('ensureDownload is idempotent within a session', async () => {
    await useYoutubeStore.getState().ensureDownload('vid-1');
    await useYoutubeStore.getState().ensureDownload('vid-1');
    await useYoutubeStore.getState().ensureDownload('vid-1');
    expect(postMock).toHaveBeenCalledTimes(1);
  });

  it('concurrent ensureDownload calls share a single POST (inFlight Map dedupe)', async () => {
    // Fire two concurrent calls before either has resolved. The inFlight
    // Map contract means the second awaiter reuses the first POST.
    const a = useYoutubeStore.getState().ensureDownload('vid-conc');
    const b = useYoutubeStore.getState().ensureDownload('vid-conc');
    await Promise.all([a, b]);
    expect(postMock).toHaveBeenCalledTimes(1);
    expect(useYoutubeStore.getState().downloaded.has('vid-conc')).toBe(true);
  });

  it('concurrent ensureDownload does NOT deadlock if one rejects', async () => {
    postMock.mockRejectedValueOnce(new Error('network blip'));
    const a = useYoutubeStore.getState().ensureDownload('vid-fail');
    const b = useYoutubeStore.getState().ensureDownload('vid-fail');
    // Both awaiters see the same rejection.
    await expect(a).rejects.toBeDefined();
    await expect(b).rejects.toBeDefined();
    // inFlight was cleaned so a retry is allowed.
    expect(useYoutubeStore.getState().inFlight.has('vid-fail')).toBe(false);
  });

  it('ensureDownload issues separate POSTs for distinct ids', async () => {
    await useYoutubeStore.getState().ensureDownload('vid-1');
    await useYoutubeStore.getState().ensureDownload('vid-2');
    expect(postMock).toHaveBeenCalledTimes(2);
    expect(useYoutubeStore.getState().downloaded.has('vid-1')).toBe(true);
    expect(useYoutubeStore.getState().downloaded.has('vid-2')).toBe(true);
  });

  it('ensureDownload is a no-op when called with empty id', async () => {
    await useYoutubeStore.getState().ensureDownload('');
    expect(postMock).not.toHaveBeenCalled();
  });

  it('does not mark failed POSTs as downloaded → retry stays available', async () => {
    postMock.mockRejectedValueOnce(new Error('network'));
    await expect(useYoutubeStore.getState().ensureDownload('vid-3')).rejects.toBeDefined();
    expect(useYoutubeStore.getState().downloaded.has('vid-3')).toBe(false);
    expect(useYoutubeStore.getState().inFlight.has('vid-3')).toBe(false);

    // Second attempt with a successful response should mark the id as
    // downloaded. Confirms "transient failure → no sticky fast-path."
    postMock.mockResolvedValueOnce({ data: { result: true, code: '200' } });
    await useYoutubeStore.getState().ensureDownload('vid-3');
    expect(useYoutubeStore.getState().downloaded.has('vid-3')).toBe(true);
    expect(postMock).toHaveBeenCalledTimes(2);
  });

  it('isDownloaded stays false until a successful POST completes', async () => {
    expect(useYoutubeStore.getState().isDownloaded('vid-4')).toBe(false);
    await useYoutubeStore.getState().ensureDownload('vid-4');
    expect(useYoutubeStore.getState().isDownloaded('vid-4')).toBe(true);
  });
});
