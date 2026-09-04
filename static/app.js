document.addEventListener('DOMContentLoaded', () => {
  let selectedFile = null;
  let currentFormat = 'markdown';
  let currentEngine = 'local';
  let currentExtractedData = null;

  // DOM Elements
  const dropzone = document.getElementById('dropzone');
  const dropzoneContent = document.getElementById('dropzone-content');
  const fileInput = document.getElementById('file-input');
  const fileView = document.getElementById('file-view');
  const fileName = document.getElementById('file-name');
  const fileSize = document.getElementById('file-size');
  const fileTypeIcon = document.getElementById('file-type-icon');
  const btnRemoveFile = document.getElementById('btn-remove-file');
  const btnConvert = document.getElementById('btn-convert');
  const convertSpinner = document.getElementById('convert-spinner');
  const btnFmtMarkdown = document.getElementById('btn-fmt-markdown');
  const btnFmtText = document.getElementById('btn-fmt-text');
  const btnEngineLocal = document.getElementById('btn-engine-local');
  const btnEngineCloud = document.getElementById('btn-engine-cloud');
  const errorBanner = document.getElementById('error-banner');

  // Result Elements
  const resultSection = document.getElementById('result-section');
  const statFormat = document.getElementById('stat-format');
  const statTime = document.getElementById('stat-time');
  const statWords = document.getElementById('stat-words');
  const statType = document.getElementById('stat-type');
  const statImagesBadge = document.getElementById('stat-images-badge');
  const statImages = document.getElementById('stat-images');
  const statVisionBadge = document.getElementById('stat-vision-badge');
  const statVisionCount = document.getElementById('stat-vision-count');
  const tabRendered = document.getElementById('tab-rendered');
  const tabRaw = document.getElementById('tab-raw');
  const tabImages = document.getElementById('tab-images');
  const tabImagesCount = document.getElementById('tab-images-count');
  const previewRendered = document.getElementById('preview-rendered');
  const previewRaw = document.getElementById('preview-raw');
  const previewImages = document.getElementById('preview-images');
  const imageGalleryGrid = document.getElementById('image-gallery-grid');
  const btnCopy = document.getElementById('btn-copy');
  const copyText = document.getElementById('copy-text');
  const btnDownload = document.getElementById('btn-download');
  const dlExt = document.getElementById('dl-ext');
  const btnToggleVision = document.getElementById('btn-toggle-vision');
  const visionBtnText = document.getElementById('vision-btn-text');
  const inputApiToken = document.getElementById('input-api-token');

  let enableAIVision = true;

  if (inputApiToken) {
    const savedToken = localStorage.getItem('c2t_api_token');
    if (savedToken) {
      inputApiToken.value = savedToken;
    }
    inputApiToken.addEventListener('input', () => {
      localStorage.setItem('c2t_api_token', inputApiToken.value.trim());
      updateCurlSnippets();
    });
  }

  if (btnToggleVision) {
    btnToggleVision.addEventListener('click', () => {
      enableAIVision = !enableAIVision;
      if (enableAIVision) {
        btnToggleVision.classList.add('active');
        if (visionBtnText) visionBtnText.textContent = 'AI Vision: Aktif (Solutioning OCR)';
      } else {
        btnToggleVision.classList.remove('active');
        if (visionBtnText) visionBtnText.textContent = 'AI Vision: Nonaktif';
      }
      updateCurlSnippets();
    });
  }

  // Engine Switchers
  if (btnEngineLocal) btnEngineLocal.addEventListener('click', () => setEngine('local'));
  if (btnEngineCloud) btnEngineCloud.addEventListener('click', () => setEngine('cloud'));

  // API Guide interactive tabs
  let activeApiTab = 'local';
  const curlDisplayCode = document.getElementById('curl-display-code');
  const curlSnippetExplanation = document.getElementById('curl-snippet-explanation');
  const btnCopyCurl = document.getElementById('btn-copy-curl');
  const copyCurlText = document.getElementById('copy-curl-text');

  const apiTabs = [
    { id: 'btn-tab-curl-local', target: 'local' },
    { id: 'btn-tab-curl-cloud', target: 'cloud' },
    { id: 'btn-tab-curl-vision', target: 'vision' },
    { id: 'btn-tab-curl-raw', target: 'raw' }
  ];

  apiTabs.forEach(tab => {
    const el = document.getElementById(tab.id);
    if (el) {
      el.addEventListener('click', () => {
        activeApiTab = tab.target;
        apiTabs.forEach(t => {
          const btn = document.getElementById(t.id);
          if (btn) btn.classList.toggle('active', t.target === activeApiTab);
        });
        updateCurlSnippets();
      });
    }
  });

  if (btnCopyCurl && curlDisplayCode) {
    btnCopyCurl.addEventListener('click', async () => {
      try {
        await navigator.clipboard.writeText(curlDisplayCode.textContent);
        if (copyCurlText) copyCurlText.textContent = 'Tersalin!';
        setTimeout(() => {
          if (copyCurlText) copyCurlText.textContent = 'Salin cURL';
        }, 2000);
      } catch (err) {
        console.error('Failed to copy curl:', err);
      }
    });
  }

  function updateCurlSnippets() {
    const token = (inputApiToken && inputApiToken.value.trim()) || 'c2t_sec_98f8a17c33d83_convert2text_token';
    const filename = selectedFile ? selectedFile.name : 'document.pdf';

    if (!curlDisplayCode) return;

    let cmd = '';
    let explanation = '';

    if (activeApiTab === 'local') {
      cmd = `curl -X POST "http://localhost:8080/api/v1/extract" \\\n`;
      cmd += `  -H "Authorization: Bearer ${token}" \\\n`;
      cmd += `  -F "file=@${filename}" \\\n`;
      cmd += `  -F "engine=local" \\\n`;
      cmd += `  -F "format=markdown"`;
      explanation = '💡 <strong>Mode Local (Default):</strong> Memproses dokumen secara offline menggunakan parser Python lokal (pdfplumber + PyMuPDF). 100% hemat Rp 0 token cost, cepat (~1.6 detik), dan privat.';
    } else if (activeApiTab === 'cloud') {
      cmd = `curl -X POST "http://localhost:8080/api/v1/extract" \\\n`;
      cmd += `  -H "Authorization: Bearer ${token}" \\\n`;
      cmd += `  -F "file=@${filename}" \\\n`;
      cmd += `  -F "engine=cloud" \\\n`;
      cmd += `  -F "format=markdown"`;
      explanation = '☁️ <strong>Mode Cloud Precision:</strong> Mengirim dokumen ke Azure Document Intelligence (Layout Model). Cocok untuk dokumen hasil scan scanner tebal, kertas miring, atau tabel dengan merged-cells sangat rumit.';
    } else if (activeApiTab === 'vision') {
      cmd = `curl -X POST "http://localhost:8080/api/v1/extract" \\\n`;
      cmd += `  -H "Authorization: Bearer ${token}" \\\n`;
      cmd += `  -F "file=@${filename}" \\\n`;
      cmd += `  -F "engine=local" \\\n`;
      cmd += `  -F "format=markdown" \\\n`;
      cmd += `  -F "ai_vision=true"`;
      explanation = '🤖 <strong>Local + Azure AI Vision:</strong> Ekstraksi teks cepat via Local Engine dan otomatis memotong diagram/topologi arsitektur untuk dianalisis oleh Azure Computer Vision 4.0.';
    } else if (activeApiTab === 'raw') {
      cmd = `curl -X POST "http://localhost:8080/api/v1/extract/raw?engine=local" \\\n`;
      cmd += `  -H "Authorization: Bearer ${token}" \\\n`;
      cmd += `  -F "file=@${filename}" \\\n`;
      cmd += `  -o output.md`;
      explanation = '📄 <strong>Raw Output Direct:</strong> Mengembalikan raw konten Markdown langsung dalam HTTP body stream (sangat ideal untuk pipeline Linux, redirect stdout, atau file saving otomatis).';
    }

    curlDisplayCode.textContent = cmd;
    if (curlSnippetExplanation) {
      curlSnippetExplanation.innerHTML = explanation;
    }
  }

  // Initialize snippets on page load
  updateCurlSnippets();

  function setEngine(eng) {
    currentEngine = eng;
    if (eng === 'local') {
      if (btnEngineLocal) btnEngineLocal.classList.add('active');
      if (btnEngineCloud) btnEngineCloud.classList.remove('active');
    } else {
      if (btnEngineCloud) btnEngineCloud.classList.add('active');
      if (btnEngineLocal) btnEngineLocal.classList.remove('active');
    }
    updateCurlSnippets();
  }

  // Format Switchers
  btnFmtMarkdown.addEventListener('click', () => setFormat('markdown'));
  btnFmtText.addEventListener('click', () => setFormat('text'));

  function setFormat(fmt) {
    currentFormat = fmt;
    if (fmt === 'markdown') {
      btnFmtMarkdown.classList.add('active');
      btnFmtText.classList.remove('active');
      dlExt.textContent = '.md';
    } else {
      btnFmtText.classList.add('active');
      btnFmtMarkdown.classList.remove('active');
      dlExt.textContent = '.txt';
    }
    updateCurlSnippets();
  }

  // Dropzone Events
  dropzone.addEventListener('click', (e) => {
    if (e.target !== btnRemoveFile && !selectedFile) {
      fileInput.click();
    }
  });

  fileInput.addEventListener('change', (e) => {
    if (e.target.files && e.target.files[0]) {
      handleFileSelected(e.target.files[0]);
    }
  });

  ['dragenter', 'dragover'].forEach(eventName => {
    dropzone.addEventListener(eventName, (e) => {
      e.preventDefault();
      e.stopPropagation();
      dropzone.classList.add('dragover');
    });
  });

  ['dragleave', 'drop'].forEach(eventName => {
    dropzone.addEventListener(eventName, (e) => {
      e.preventDefault();
      e.stopPropagation();
      dropzone.classList.remove('dragover');
    });
  });

  dropzone.addEventListener('drop', (e) => {
    const dt = e.dataTransfer;
    if (dt && dt.files && dt.files[0]) {
      handleFileSelected(dt.files[0]);
    }
  });

  btnRemoveFile.addEventListener('click', (e) => {
    e.stopPropagation();
    resetFileInput();
  });

  function handleFileSelected(file) {
    hideError();
    const ext = file.name.split('.').pop().toLowerCase();
    const allowed = ['pdf', 'docx', 'xlsx', 'xls', 'pptx'];

    if (!allowed.includes(ext)) {
      showError(`Format file .${ext} tidak didukung. Harap upload file PDF, DOCX, XLSX, atau PPTX.`);
      return;
    }

    selectedFile = file;
    fileName.textContent = file.name;
    fileSize.textContent = formatBytes(file.size);
    fileTypeIcon.textContent = ext.toUpperCase();

    dropzoneContent.classList.add('hidden');
    fileView.classList.remove('hidden');
    btnConvert.disabled = false;
    updateCurlSnippets();
  }

  function resetFileInput() {
    selectedFile = null;
    fileInput.value = '';
    dropzoneContent.classList.remove('hidden');
    fileView.classList.add('hidden');
    btnConvert.disabled = true;
    hideError();
    updateCurlSnippets();
  }

  function formatBytes(bytes) {
    if (bytes === 0) return '0 B';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
  }

  function showError(msg) {
    errorBanner.textContent = msg;
    errorBanner.classList.remove('hidden');
  }

  function hideError() {
    errorBanner.textContent = '';
    errorBanner.classList.add('hidden');
  }

  // Convert Action
  btnConvert.addEventListener('click', async () => {
    if (!selectedFile) return;

    hideError();
    btnConvert.disabled = true;
    convertSpinner.classList.remove('hidden');

    const formData = new FormData();
    formData.append('file', selectedFile);
    formData.append('format', currentFormat);
    formData.append('engine', currentEngine);
    formData.append('ai_vision', enableAIVision ? 'true' : 'false');

    const headers = {};
    const apiToken = (inputApiToken && inputApiToken.value.trim()) || localStorage.getItem('c2t_api_token');
    if (apiToken) {
      headers['Authorization'] = `Bearer ${apiToken}`;
    }

    try {
      const response = await fetch('/api/v1/extract', {
        method: 'POST',
        headers: headers,
        body: formData,
      });

      const result = await response.json();

      if (!response.ok || !result.success) {
        throw new Error(result.error || 'Terjadi kesalahan saat memproses ekstraksi file.');
      }

      displayResult(result.data);
    } catch (err) {
      showError(err.message || 'Gagal menghubungi server.');
    } finally {
      btnConvert.disabled = false;
      convertSpinner.classList.add('hidden');
    }
  });

  function displayResult(data) {
    currentExtractedData = data;
    resultSection.classList.remove('hidden');

    statFormat.textContent = data.output_format.toUpperCase();
    statTime.textContent = data.duration_ms;
    statWords.textContent = (data.word_count || 0).toLocaleString();
    statType.textContent = (data.detected_type || 'auto').toUpperCase();

    // Image stats and gallery rendering
    const images = data.images || [];
    const visionAnalyzedCount = (data.metadata && data.metadata.ai_vision_analyzed) || 0;

    if (statVisionBadge && statVisionCount) {
      if (visionAnalyzedCount > 0) {
        statVisionCount.textContent = visionAnalyzedCount;
        statVisionBadge.classList.remove('hidden');
      } else {
        statVisionBadge.classList.add('hidden');
      }
    }

    if (images.length > 0) {
      statImagesBadge.classList.remove('hidden');
      statImages.textContent = images.length;
      tabImages.classList.remove('hidden');
      tabImagesCount.textContent = images.length;
      dlExt.textContent = '.zip Bundle';

      // Populate Image Gallery
      imageGalleryGrid.innerHTML = '';
      images.forEach((img, idx) => {
        const card = document.createElement('div');
        card.className = 'gallery-card';

        const hasVision = !!img.vision_analysis;
        const visionData = img.vision_analysis;

        let visionSectionHtml = '';
        if (hasVision) {
          let tagsHtml = '';
          if (visionData.tags && visionData.tags.length > 0) {
            tagsHtml = `
              <div class="gallery-tags-list">
                ${visionData.tags.map(t => `<span class="gallery-tag-pill">${escapeHtml(t)}</span>`).join('')}
              </div>
            `;
          }

          let ocrHtml = '';
          if (visionData.extracted_text && visionData.extracted_text.length > 0) {
            ocrHtml = `
              <div class="gallery-ocr-text" title="Teks / Diagram OCR Inscriptions">${escapeHtml(visionData.extracted_text.join('\n'))}</div>
            `;
          }

          let objectsHtml = '';
          if (visionData.objects && visionData.objects.length > 0) {
            objectsHtml = `<div class="gallery-card-alt"><strong>Komponen:</strong> ${escapeHtml(visionData.objects.join(', '))}</div>`;
          }

          visionSectionHtml = `
            <div class="gallery-vision-section">
              <div class="gallery-vision-title">🤖 Insight AI Vision (Solutioning)</div>
              ${tagsHtml}
              ${objectsHtml}
              ${ocrHtml}
            </div>
          `;
        }

        card.innerHTML = `
          <div class="gallery-card-preview">
            ${hasVision ? `<span class="gallery-vision-badge">🤖 AI Vision</span>` : ''}
            ${img.url ? `<img src="${img.url}" alt="${escapeHtml(img.alt_text || img.filename)}" loading="lazy">` : '<div class="gallery-placeholder-icon">🖼️</div>'}
          </div>
          <div class="gallery-card-body">
            <div class="gallery-card-title">${escapeHtml(img.filename || `Gambar ${idx + 1}`)}</div>
            ${img.alt_text ? `<div class="gallery-card-alt">${escapeHtml(img.alt_text)}</div>` : ''}
            ${visionSectionHtml}
            <div class="gallery-card-meta">
              <span>${escapeHtml(img.location || 'Embedded')}</span>
              ${img.url ? `<a href="${img.url}" target="_blank" download="${escapeHtml(img.filename)}" class="gallery-download-btn">Unduh</a>` : ''}
            </div>
          </div>
        `;
        imageGalleryGrid.appendChild(card);
      });
    } else {
      statImagesBadge.classList.add('hidden');
      tabImages.classList.add('hidden');
      imageGalleryGrid.innerHTML = '';
      dlExt.textContent = data.output_format === 'markdown' ? '.md' : '.txt';
    }

    // Fill Raw Content
    previewRaw.value = data.content;

    // Fill Rendered Content
    if (data.output_format === 'markdown' && typeof marked !== 'undefined') {
      previewRendered.innerHTML = marked.parse(data.content);
    } else {
      // Plain text formatting
      previewRendered.innerHTML = `<pre style="white-space: pre-wrap; font-family: var(--font-mono);">${escapeHtml(data.content)}</pre>`;
    }

    // Default view is rendered
    showTab('rendered');

    // Scroll to results smoothly
    resultSection.scrollIntoView({ behavior: 'smooth', block: 'start' });
  }

  function escapeHtml(text) {
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
  }

  // Tab switching
  tabRendered.addEventListener('click', () => showTab('rendered'));
  tabRaw.addEventListener('click', () => showTab('raw'));
  tabImages.addEventListener('click', () => showTab('images'));

  function showTab(type) {
    tabRendered.classList.remove('active');
    tabRaw.classList.remove('active');
    tabImages.classList.remove('active');
    previewRendered.classList.add('hidden');
    previewRaw.classList.add('hidden');
    previewImages.classList.add('hidden');

    if (type === 'rendered') {
      tabRendered.classList.add('active');
      previewRendered.classList.remove('hidden');
    } else if (type === 'raw') {
      tabRaw.classList.add('active');
      previewRaw.classList.remove('hidden');
    } else if (type === 'images') {
      tabImages.classList.add('active');
      previewImages.classList.remove('hidden');
    }
  }

  // Copy Action
  btnCopy.addEventListener('click', async () => {
    if (!currentExtractedData) return;
    try {
      await navigator.clipboard.writeText(currentExtractedData.content);
      const originalText = copyText.textContent;
      copyText.textContent = 'Tersalin!';
      btnCopy.style.background = 'rgba(16, 185, 129, 0.2)';
      setTimeout(() => {
        copyText.textContent = originalText;
        btnCopy.style.background = '';
      }, 2000);
    } catch (e) {
      alert('Gagal menyalin teks secara otomatis.');
    }
  });

  // Download Action (ZIP Bundle if images exist, or single file)
  btnDownload.addEventListener('click', async () => {
    if (!currentExtractedData) return;
    const isMd = currentExtractedData.output_format === 'markdown';
    const docExt = isMd ? '.md' : '.txt';
    const baseName = currentExtractedData.filename ? currentExtractedData.filename.replace(/\.[^/.]+$/, '') : 'extracted';

    // If document contains images and source file is available, download full ZIP Bundle
    if (currentExtractedData.images && currentExtractedData.images.length > 0 && selectedFile) {
      const originalHtml = btnDownload.innerHTML;
      try {
        btnDownload.disabled = true;
        btnDownload.innerHTML = '<span>Mengemas ZIP...</span>';

        const formData = new FormData();
        formData.append('file', selectedFile);
        formData.append('format', currentExtractedData.output_format);

        const resp = await fetch('/api/v1/extract/bundle', {
          method: 'POST',
          body: formData,
        });

        if (resp.ok) {
          const blob = await resp.blob();
          const url = URL.createObjectURL(blob);
          const a = document.createElement('a');
          a.href = url;
          a.download = `${baseName}.zip`;
          document.body.appendChild(a);
          a.click();
          document.body.removeChild(a);
          URL.revokeObjectURL(url);
          return;
        }
      } catch (e) {
        console.warn('Fallback to markdown download:', e);
      } finally {
        btnDownload.disabled = false;
        btnDownload.innerHTML = originalHtml;
      }
    }

    // Fallback or single file download
    downloadSingleFile(baseName, docExt, isMd);
  });

  function downloadSingleFile(baseName, ext, isMd) {
    const mime = isMd ? 'text/markdown;charset=utf-8;' : 'text/plain;charset=utf-8;';
    const fileName = `${baseName}${ext}`;
    const blob = new Blob([currentExtractedData.content], { type: mime });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = fileName;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
  }
});
