export interface Worker {
  worker_id: string;
  hostname: string;
  token_expired_time: string | null;
  status: 'idle' | 'busy' | 'offline';
  last_heartbeat: string;
  current_job_id: string | null;
  paused: number;
}
