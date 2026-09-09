# My T Companion 1.10.46

- 修復 HostBox／命令列升級 1.10.45 失敗。安裝程式現在會把
  `internal/friendtogether` 拷進 Docker 建置目錄，不再出現
  `"/internal": not found`。
- 朋友同行仍預設關閉。常規升級不改變停車、導航、配對、APNs 與原有能力。
- 1.10.45 建置失敗時已自動回滾；本版是從該回滾繼續升級的路徑。

不需要資料庫、Tesla 權杖或既有資料遷移。
