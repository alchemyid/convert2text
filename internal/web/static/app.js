document.addEventListener('DOMContentLoaded', () => {
  let selectedFile = null;
  let currentFormat = 'markdown';
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
  const errorBanner = document.getElementById('error-banner');

  // Result Elements
  const resultSection = document.getElementById('result-section');
  const statFormat = document.getElementById('stat-format');
  const statTime = document.getElementById('stat-time');
  const statWords = document.getElementById('stat-words');
  const statType = document.getElementById('stat-type');
  const statImagesBadge = document.getElementById('stat-images-badge');
  const statImages = document.getElementById('stat-images');
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
  }

  function resetFileInput() {
    selectedFile = null;
    fileInput.value = '';
    dropzoneContent.classList.remove('hidden');
    fileView.classList.add('hidden');
    btnConvert.disabled = true;
    hideError();
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

    try {
      const response = await fetch('/api/v1/extract', {
        method: 'POST',
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
        card.innerHTML = `
          <div class="gallery-card-preview">
            ${img.url ? `<img src="${img.url}" alt="${escapeHtml(img.alt_text || img.filename)}" loading="lazy">` : '<div class="gallery-placeholder-icon">🖼️</div>'}
          </div>
          <div class="gallery-card-body">
            <div class="gallery-card-title">${escapeHtml(img.filename || `Gambar ${idx + 1}`)}</div>
            ${img.alt_text ? `<div class="gallery-card-alt">${escapeHtml(img.alt_text)}</div>` : ''}
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
