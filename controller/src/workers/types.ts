export interface Worker {
  worker_id: string;
  hostname: string;
  status: 'idle' | 'busy' | 'offline';
  last_heartbeat: string;
  current_job_id: string | null;
}
