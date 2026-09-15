export type JobStatus = 'queued' | 'running' | 'succeeded' | 'failed';

export interface Job {
  id: string;
  status: JobStatus;
  command: string[];
  created_at: string;
  started_at: string | null;
  finished_at: string | null;
  worker_id: string | null;
  progress: number;
  exit_code: number | null;
  error: string | null;
  output_path: string | null;
  hostname: string | null;
  cancel_requested: number;
}
