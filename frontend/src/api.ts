export const targetFormats = ['webp', 'jpg', 'png', 'tiff', 'heic'] as const;

export type TargetFormat = (typeof targetFormats)[number];
export type JobStatus = 'queued' | 'running' | 'done' | 'failed' | string;

export type CreateJobResponse = {
  job_id: string;
  status: JobStatus;
};

export type JobStatusResponse = {
  job_id: string;
  status: JobStatus;
  download_url?: string;
  error_code?: string;
  error_message?: string;
};

export class ApiError extends Error {
  readonly status: number;

  constructor(status: number, message: string) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
  }
}

async function parseResponse<T>(response: Response): Promise<T> {
  const contentType = response.headers.get('content-type') ?? '';
  const body = contentType.includes('application/json')
    ? await response.json()
    : await response.text();

  if (!response.ok) {
    const message =
      typeof body === 'object' && body !== null && 'error' in body
        ? String(body.error)
        : response.statusText || 'Request failed';
    throw new ApiError(response.status, message);
  }

  return body as T;
}

function authHeaders(apiKey: string): HeadersInit {
  return {
    Authorization: `ApiKey ${apiKey}`,
  };
}

export async function createJob(params: {
  apiKey: string;
  file: File;
  target: TargetFormat;
}): Promise<CreateJobResponse> {
  const form = new FormData();
  form.append('file', params.file);
  form.append('target', params.target);

  const response = await fetch('/v1/jobs', {
    method: 'POST',
    headers: authHeaders(params.apiKey),
    body: form,
  });

  return parseResponse<CreateJobResponse>(response);
}

export async function getJob(params: {
  apiKey: string;
  jobId: string;
}): Promise<JobStatusResponse> {
  const response = await fetch(`/v1/jobs/${encodeURIComponent(params.jobId)}`, {
    headers: authHeaders(params.apiKey),
  });

  return parseResponse<JobStatusResponse>(response);
}
