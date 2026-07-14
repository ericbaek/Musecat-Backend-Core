export interface UploadSummary {
  total: number;
  success: number;
  failed: number;
}

export interface UploadedPhotoItem {
  index: number;
  atomId: string;
  filename: string;
}

export interface FailedPhotoItem {
  index: number;
  filename: string;
  reason: string;
}

export interface ArcadePhotoUploadResponse {
  arcade: string;
  summary: UploadSummary;
  uploaded: UploadedPhotoItem[];
  failed: FailedPhotoItem[];
}

export interface ArcadePhotoUploadError {
  error: string;
  details?: string;
}

export async function uploadArcadePhotos(
  baseUrl: string,
  token: string,
  arcadeId: string,
  files: File[],
  onProgress?: (percent: number) => void,
): Promise<ArcadePhotoUploadResponse> {
  const url = new URL("/arcade/photo/upload", baseUrl);
  const formData = new FormData();
  formData.append("arcade", arcadeId);
  for (const file of files) {
    formData.append("photos", file);
  }

  return await new Promise<ArcadePhotoUploadResponse>((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("POST", url.toString());
    xhr.setRequestHeader("Authorization", `Bearer ${token}`);

    xhr.upload.onprogress = (event: ProgressEvent) => {
      if (!onProgress || !event.lengthComputable || event.total === 0) return;
      const percent = Math.round((event.loaded / event.total) * 100);
      onProgress(percent);
    };

    xhr.onerror = () => {
      reject({ error: "network error" } as ArcadePhotoUploadError);
    };

    xhr.onload = () => {
      try {
        const body = JSON.parse(xhr.responseText || "{}") as
          | ArcadePhotoUploadResponse
          | ArcadePhotoUploadError;

        if (xhr.status >= 200 && xhr.status < 300) {
          resolve(body as ArcadePhotoUploadResponse);
          return;
        }

        reject(body);
      } catch {
        reject({ error: "invalid JSON response" } as ArcadePhotoUploadError);
      }
    };

    xhr.send(formData);
  });
}
