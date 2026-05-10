import APIService from '$lib/services/api.service';

export interface AuditLogEntry {
	id: string;
	request_id: string;
	decision: 'approved' | 'rejected';
	account: string;
	cert_type: string;
	created_at: string;
}

export interface AuditLogResponse {
	status: string;
	entries: AuditLogEntry[];
}

export default class AuditLogService extends APIService {
	list = async (): Promise<AuditLogEntry[]> => {
		const res = await this.api.get<AuditLogResponse>('/v1/audit');
		return res.data.entries ?? [];
	};
}
