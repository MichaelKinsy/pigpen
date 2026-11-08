;;;; autolith-bridge: run Autolith (https://autolith.rocks, by Lambda Symbolics) headless jobs from PiG.
;;;; Autolith is a separate program; this extension only calls its documented `autolith run-job` interface and reads its S-expression events.

(defpackage "autolith-bridge" (:use "COMMON-LISP"))
(in-package "autolith-bridge")

;;; Reading Autolith data: top-level forms, *READ-EVAL* NIL, never line by line (a string may hold newlines).

(defpackage "autolith-bridge-data" (:use "COMMON-LISP"))

(defun read-data-forms (text start)
  "The complete top-level forms of TEXT from index START, as data. (values forms end): END is where the next call should start;
a form still being written (end of text inside it) is left for that call."
  (let ((*read-eval* nil) (*package* (find-package "autolith-bridge-data")) (forms '()) (end start))
    (loop
      (multiple-value-bind (form next) (handler-case (read-from-string text nil '%eof :start end)
                                         (end-of-file () (return)))
        (when (eq form '%eof) (return))
        (push form forms)
        (setf end next)))
    (values (nreverse forms) end)))

(defun slurp (path)
  (with-open-file (in path :if-does-not-exist nil :external-format :utf-8)
    (if in
        (let* ((text (make-string (file-length in))) (n (read-sequence text in))) (subseq text 0 n))
        "")))

(defun label (x) (if (stringp x) x (string-downcase (princ-to-string x))))

;;; Run state, folded from the event stream.

(defstruct run
  (id "") (state "starting") (tool nil) (seq 0)
  (tools-started 0) (tools-finished 0) (tools-failed 0)
  (progress 0) (warnings 0) (omitted 0)
  (usage (make-hash-table :test 'equal))     ; job id -> (input-tokens output-tokens requests)
  (notes '()) (answer nil)                   ; a session's tool results and its final text
  (calls (make-hash-table :test 'equal))     ; tool call id -> :running or :done (Autolith reports a finished call twice)
  (started (get-internal-real-time)))

(defun run-seconds (run) (floor (- (get-internal-real-time) (run-started run)) internal-time-units-per-second))

(defun run-total (run index) (loop for v being the hash-values of (run-usage run) sum (nth index v)))

(defun apply-event (run event)
  "Fold one (:autolith-event :version 1 ... :kind K :data D) record into RUN. Other shapes and unknown kinds are ignored."
  (when (and (consp event) (eq (first event) :autolith-event) (eql (getf (rest event) :version) 1))
    (let ((data (getf (rest event) :data)))
      (setf (run-seq run) (max (run-seq run) (or (getf (rest event) :seq) 0)))
      (case (getf (rest event) :kind)
        (:run-started (setf (run-state run) "starting" (run-tool run) nil   ; a new run: Autolith's launcher runs a failed job once more on clean source
                            (run-tools-started run) 0 (run-tools-finished run) 0 (run-tools-failed run) 0 (run-progress run) 0
                            (run-warnings run) 0 (run-omitted run) 0)
         (clrhash (run-usage run)) (clrhash (run-calls run)))
        (:job-started (setf (run-state run) "running"))
        (:tool-started (setf (run-tool run) (label (getf data :tool-name)))
         (unless (gethash (getf data :call-id) (run-calls run))
           (setf (gethash (getf data :call-id) (run-calls run)) :running)
           (incf (run-tools-started run))))
        (:tool-finished (setf (run-tool run) nil)
         (unless (eq (gethash (getf data :call-id) (run-calls run)) :done)
           (setf (gethash (getf data :call-id) (run-calls run)) :done)
           (incf (run-tools-finished run))
           (unless (member (getf data :status) '(:succeeded :success)) (incf (run-tools-failed run)))))
        (:progress (incf (run-progress run)))
        (:usage (let ((total (or (gethash (getf data :job-id) (run-usage run)) (list 0 0 0))))   ; tokens of each request add up; the request count is already a running count
                  (setf (gethash (getf data :job-id) (run-usage run))
                        (list (+ (first total) (or (getf data :input-tokens) 0)) (+ (second total) (or (getf data :output-tokens) 0))
                              (max (third total) (or (getf data :provider-requests) 0))))))
        (:warning (incf (run-warnings run)))
        (:progress-omitted (incf (run-omitted run) (or (getf data :count) 0)))
        (:run-finished (setf (run-state run) (label (getf data :status)) (run-tool run) nil))))))

(defun widget-lines (run)
  (list (format nil "autolith ~A   ~A   ~Ds" (run-id run) (run-state run) (run-seconds run))
        (format nil "  tool ~A   tools ~D/~D~@[ (~D failed)~]   progress ~D   requests ~D   tokens ~D in ~D out"
                (or (run-tool run) "-") (run-tools-finished run) (run-tools-started run)
                (and (plusp (run-tools-failed run)) (run-tools-failed run))
                (run-progress run) (run-total run 2) (run-total run 0) (run-total run 1))))

(defvar *last-run* nil "The run the widget and status line describe.")

(defun show-status (ctx run)
  (pig:ctx-set-status ctx "autolith" (format nil "autolith: ~A~@[ ~A~] (~D tools)" (run-state run) (run-tool run) (run-tools-started run))))

(defun show (ctx run)
  (pig:ctx-set-widget ctx "autolith" (widget-lines run))
  (show-status ctx run))

(defun file-poller (read-text apply)
  "A function that passes each new complete top-level form of the text READ-TEXT returns to APPLY."
  (let ((pos 0))
    (lambda ()
      (let ((text (funcall read-text)))
        (handler-case (multiple-value-bind (forms end) (read-data-forms text pos)
                        (setf pos end)
                        (mapc apply forms))
          (reader-error () (setf pos (length text))))))))

(defun spawn-watch (ctx run stop poll)
  "A thread that calls POLL, which folds new data into RUN, every quarter second and shows RUN, until (car STOP) is true; then once more."
  (sb-thread:make-thread
   (lambda ()
     (let ((shown nil))
       (loop
         (let ((done (car stop)))
           (handler-case (funcall poll) (error () (incf (run-warnings run))))
           (let ((lines (widget-lines run)))
             (unless (equal lines shown) (setf shown lines) (show ctx run)))
           (when done (return))
           (sleep 0.25)))))
   :name "autolith-watch"))

;;; The job file and its result.

(defparameter *contract*
  '(:type :object
    :properties (("summary" (:type :string)) ("evidence" (:type :string)))
    :required ("summary" "evidence")
    :additional-properties nil))

(defun write-job (path id role prompt timeout)
  (with-open-file (out path :direction :output :if-exists :supersede :external-format :utf-8)
    (let ((*print-readably* nil) (*print-pretty* nil) (*package* (find-package "autolith-bridge-data")))   ; NIL must print unqualified: Autolith refuses package-qualified symbols
      (prin1 `(:autolith-job :version 1 :id ,id :role ,role :prompt ,prompt :input nil
               :output-contract ,*contract* :timeout-seconds ,timeout)
             out))))

(defun render-result (value)
  "Autolith tags a structured object (:object (\"key\" value) ...); show each member."
  (if (and (consp value) (eq (first value) :object))
      (format nil "~{~A~^~%~%~}" (loop for (key v) in (rest value) collect (format nil "~A:~%~A" key (render-result v))))
      (if (stringp value) value (let ((*print-pretty* nil)) (prin1-to-string value)))))

(defun tail (text n) (subseq text (max 0 (- (length text) n))))

;;; One job.

(defparameter *launcher*
  "set -m; events=$1; diag=$2; shift 2
\"$@\" >\"$events\" 2>\"$diag\" & pid=$!
trap 'kill -TERM -- -$pid 2>/dev/null; { sleep 4; kill -KILL -- -$pid 2>/dev/null; } & wait $pid; exit 143' TERM
wait $pid; exit $?"
  "Runs Autolith in its own process group and passes the host's SIGTERM (Esc) to the whole group. bash, because `set -m` gives the job its own group (dash refuses without a tty). Autolith's launcher is a bash script that
keeps its SBCL child and sandboxed shell commands running when it alone is signalled.")

(defun run-autolith-job (ctx prompt role timeout permissions)
  "Run an Autolith job and return the tool result. Cancelling the request (Esc) kills the process; the result file then says why."
  (let* ((dir (string-right-trim '(#\Newline) (pig:exec-result-stdout (pig:ctx-exec ctx "mktemp" :args '("-d" "-t" "autolith-bridge.XXXXXX")))))
         (id (format nil "pig-~(~36R~)" (random (expt 36 6) (make-random-state t))))
         (run (setf *last-run* (make-run :id id))) (stop (list nil))
         (events (concatenate 'string dir "/events.sexp")) (diag (concatenate 'string dir "/diagnostics.log"))
         (job (concatenate 'string dir "/job.sexp")) (out (concatenate 'string dir "/result.sexp"))
         (watcher nil))
    (unwind-protect
         (progn
           (write-job job id role prompt timeout)
           (setf watcher (spawn-watch ctx run stop (file-poller (lambda () (slurp events)) (lambda (event) (apply-event run event)))))
           ;; exec returns the output when the process ends, so the events go to a file and the watcher reads them as they come
           (let* ((exec (pig:ctx-exec ctx "bash"
                                      :args (list "-c" *launcher* "bash" events diag
                                                  "autolith" "--permissions" permissions "run-job" "--input" job "--output" out "--events" "sexp")
                                      :timeout (* 1000 (+ timeout 60))))
                  (result (let ((forms (read-data-forms (slurp out) 0))) (first forms))))
             (setf (car stop) t)
             (sb-thread:join-thread watcher :default nil)
             (setf watcher nil)
             (outcome run exec result (slurp diag) (pig:ctx-cancelled-p ctx))))
      (setf (car stop) t)
      (when watcher (sb-thread:join-thread watcher :default nil))
      (when (member (run-state run) '("starting" "running") :test #'string=)   ; no :run-finished arrived: the process was killed
        (setf (run-state run) (if (pig:ctx-cancelled-p ctx) "cancelled" "stopped") (run-tool run) nil)
        (pig:ctx-set-widget ctx "autolith" (widget-lines run)))   ; a cancelled request can no longer set the status; :agent-end does
      (ignore-errors (sb-ext:delete-directory dir :recursive t)))))

(defun outcome (run exec result diag cancelled)
  (let* ((plist (rest result)) (status (getf plist :status)) (failure (getf plist :failure))
         (details (pig:obj "runId" (run-id run) "tools" (run-tools-started run) "requests" (run-total run 2)
                           "inputTokens" (run-total run 0) "outputTokens" (run-total run 1) "seconds" (run-seconds run))))
    (cond ((eq status :succeeded)
           (pig:tool-result (render-result (getf plist :result)) :details details))
          (result
           (pig:tool-result (format nil "Autolith job ~A: ~A (category ~A): ~A" (run-id run) (label status)
                                    (label (getf failure :category)) (or (getf failure :message) "no message"))
                            :is-error t :details details))
          (cancelled
           (pig:tool-result (format nil "Autolith job ~A was cancelled before it wrote a result." (run-id run)) :is-error t :details details))
          (t
           (pig:tool-result (format nil "Autolith exited with status ~D and wrote no result.~@[ Its diagnostics ended:~%~A~]"
                                    (pig:exec-result-exit-code exec) (let ((d (string-trim '(#\Newline #\Space) (tail diag 600)))) (and (plusp (length d)) d)))
                            :is-error t :details details)))))

;;; The other way in: a primary Autolith session. Its self.* tools (redefine, exercise, commit) are the ones a run-job child never has
;;; ("self.* tools are deliberately unavailable"), so Autolith can change its own running image only here. A session shows no sexp events;
;;; its state comes from `autolith localgroup status --sexp` and its tool results from its conversation file under the data directory.

(defun data-directory ()
  (let ((xdg (sb-ext:posix-getenv "XDG_DATA_HOME")))
    (format nil "~A/autolith" (if (and xdg (plusp (length xdg)) (char= (char xdg 0) #\/)) xdg (format nil "~A/.local/share" (sb-ext:posix-getenv "HOME"))))))

(defun conversation-text (id)
  "The segments of conversation ID, in order."
  (let ((files (sort (directory (format nil "~A/conversations/~A/*.sexp" (data-directory) id)) #'string< :key #'namestring)))
    (apply #'concatenate 'string (mapcar #'slurp files))))

(defun apply-record (run record)
  "Fold one conversation record into RUN: a tool call starts a tool, its :tool-result ends it, a :provider record carries one request's usage,
and the last assistant message with text is the answer."
  (flet ((tool-name (x) (and (stringp x) x)))
    (case (first record)
      (:tool-result
       (let ((tool (tool-name (getf (rest record) :tool))) (output (getf (rest record) :output)))
         (setf (run-tool run) nil)
         (incf (run-tools-finished run))
         (unless (eq (getf (rest record) :status) :ok) (incf (run-tools-failed run)))
         (setf (run-notes run) (append (run-notes run)
                                       (list (format nil "~A ~(~A~): ~A" tool (getf (rest record) :status)
                                                     (if (stringp output) (subseq output 0 (position #\Newline output :end (min (length output) 160))) "")))))))
      (:provider-item
       (let ((json (getf (rest record) :wire-json)))
         (when (stringp json)
           (cond ((search "\"type\":\"function_call\"," json)
                  (setf (run-tool run) (or (tool-name (getf (rest record) :tool-label)) "tool"))
                  (incf (run-tools-started run)))
                 ((and (search "\"role\":\"assistant\"" json) (search "\"output_text\"" json))
                  (let ((parts (loop for part across (pig:jget (pig:from-json json) "content")
                                     when (equal (pig:jget part "type") "output_text") collect (pig:jget part "text"))))
                    (when parts (setf (run-answer run) (format nil "~{~A~^~%~}" parts)))))))))
      (:provider
       (let* ((meta (getf (rest record) :metadata)) (usage (getf meta :usage)))
         (setf (gethash "session" (run-usage run))
               (list (+ (or (first (gethash "session" (run-usage run))) 0) (or (second (assoc "input_tokens" usage :test #'equal)) 0))
                     (+ (or (second (gethash "session" (run-usage run))) 0) (or (second (assoc "output_tokens" usage :test #'equal)) 0))
                     (max (or (getf meta :request-number) 0) (or (third (gethash "session" (run-usage run))) 0)))))))))

(defun session-id (text)
  "The session id in `Conversation 3-2XYLzs  PID 123`: the displayed id without its hyphen."
  (let ((start (search "Conversation " text)))
    (when start
      (let* ((from (+ start 13)) (end (or (position-if (lambda (c) (member c '(#\Space #\Newline))) text :start from) (length text))))
        (remove #\- (subseq text from end))))))

(defun session-state (ctx id)
  "The :STATE of session ID from `autolith localgroup status --sexp` as a lower-case string, or NIL."
  (let ((status (pig:exec-result-stdout (pig:ctx-exec ctx "autolith" :args '("localgroup" "status" "--sexp")))))
    (dolist (form (ignore-errors (read-data-forms status 0)))
      (when (and (consp form) (equal (getf (rest form) :session-id) id))
        (return (label (getf (rest form) :state)))))))

(defun kill-session (id)
  "End the session. A run-program, not ctx-exec: after Esc the request is cancelled and the host refuses further calls."
  (ignore-errors (sb-ext:run-program "autolith" (list "localgroup" "kill" id) :search t :output nil :error nil :input nil)))

(defun run-autolith-session (ctx prompt model timeout)
  (let* ((started (pig:exec-result-stdout (pig:ctx-exec ctx "autolith" :args (append (list "--detached" "--permissions" "auto") (and model (list "--model" model))))))
         (id (or (session-id started) (error "autolith --detached printed no conversation id: ~A" started)))
         (run (setf *last-run* (make-run :id id))) (stop (list nil)) (watcher nil))
    (unwind-protect
         (progn
           (setf watcher (spawn-watch ctx run stop (file-poller (lambda () (conversation-text id)) (lambda (record) (apply-record run record)))))
           (pig:ctx-exec ctx "autolith" :args (list "localgroup" "tell" id prompt))
           (let ((busy nil) (deadline (+ (get-internal-real-time) (* timeout internal-time-units-per-second))))
             (loop
               (let ((state (session-state ctx id)))
                 (when state (setf (run-state run) state))
                 (when (and state (string/= state "idle")) (setf busy t))
                 (when (and busy (equal state "idle")) (return))
                 (when (> (get-internal-real-time) deadline) (setf (run-state run) "timed-out") (return))
                 (sleep 1.5))))
           (setf (car stop) t)
           (sb-thread:join-thread watcher :default nil)
           (setf watcher nil)
           (let ((details (pig:obj "sessionId" id "tools" (run-tools-started run) "requests" (run-total run 2)
                                   "inputTokens" (run-total run 0) "outputTokens" (run-total run 1) "seconds" (run-seconds run))))
             (cond ((equal (run-state run) "timed-out")
                    (pig:tool-result (format nil "Autolith session ~A did not finish within ~D seconds." id timeout) :is-error t :details details))
                   ((run-answer run)
                    (pig:tool-result (format nil "~A~%~%Tool results:~%~{~A~^~%~}" (run-answer run) (run-notes run)) :details details))
                   (t (pig:tool-result (format nil "Autolith session ~A went idle without an answer." id) :is-error t :details details)))))
      (setf (car stop) t)
      (when watcher (sb-thread:join-thread watcher :default nil))
      (when (member (run-state run) '("starting" "working" "active" "idle") :test #'string=)
        (setf (run-state run) (if (pig:ctx-cancelled-p ctx) "cancelled" "done") (run-tool run) nil)
        (pig:ctx-set-widget ctx "autolith" (widget-lines run)))
      (kill-session id))))

;;; The extension.

(defun string-param (params key) (let ((v (gethash key params))) (and (stringp v) (plusp (length v)) v)))

(pig:define-extension "autolith-bridge"
  (:tool "autolith_job"
         "Hand a job to Autolith (Lambda Symbolics' Common Lisp coding agent) and wait for its result. It runs as a headless child role with its own tools and model, shows live progress in a widget, and returns a summary and the evidence. Esc cancels it."
         (pig:obj "type" "object"
                  "properties" (pig:obj "prompt" (pig:obj "type" "string" "description" "What Autolith should do.")
                                        "role" (pig:obj "type" "string" "description" "Autolith child role: task (default), engineering, scout, reviewer, librarian, designer, sonic.")
                                        "timeout" (pig:obj "type" "integer" "description" "Seconds Autolith may take (default 900).")
                                        "permissions" (pig:obj "type" "string" "enum" #("auto" "sandbox")
                                                               "description" "auto (default) denies shell commands that need approval; sandbox runs shell commands in Autolith's sandbox."))
                  "required" #("prompt"))
         (lambda (ctx params)
           (run-autolith-job ctx (or (string-param params "prompt") (error "prompt is required"))
                    (or (string-param params "role") "task")
                    (let ((n (gethash "timeout" params))) (if (and (integerp n) (plusp n)) n 900))
                    (if (equal (gethash "permissions" params) "sandbox") "sandbox" "auto"))))
  (:tool "autolith_session"
         "Hand a task to a primary Autolith session, the only kind with the self.redefine, self.exercise and self.commit tools: it changes its own running Lisp image, checks the change and commits it as a private image commit. It takes about a minute to start, then answers."
         (pig:obj "type" "object"
                  "properties" (pig:obj "prompt" (pig:obj "type" "string" "description" "What Autolith should change in itself.")
                                        "model" (pig:obj "type" "string" "description" "Autolith model, for example copilot/claude-sonnet-5.5 (default: Autolith's own default).")
                                        "timeout" (pig:obj "type" "integer" "description" "Seconds to wait (default 300)."))
                  "required" #("prompt"))
         (lambda (ctx params)
           (run-autolith-session ctx (or (string-param params "prompt") (error "prompt is required"))
                                 (string-param params "model")
                                 (let ((n (gethash "timeout" params))) (if (and (integerp n) (plusp n)) n 300)))))
  (:command "autolith" "Run an Autolith job from the prompt: /autolith [role=NAME] what to do"
            (lambda (ctx args)
              (let* ((text (string-trim " " args))
                     (role (and (> (length text) 5) (string= "role=" text :end2 5)
                                (subseq text 5 (or (position #\Space text) (length text)))))
                     (prompt (if role (string-left-trim " " (subseq text (or (position #\Space text) (length text)))) text)))
                (if (zerop (length prompt))
                    (pig:ctx-notify ctx "usage: /autolith [role=NAME] what to do" "warning")
                    (let ((result (run-autolith-job ctx prompt (or role "task") 900 "auto")))
                      (pig:ctx-send-message ctx "autolith" (gethash "content" result)))))))
  (:on :agent-end
       (lambda (ctx data)
         (declare (ignore data))
         (when *last-run* (show-status ctx *last-run*))))
  (:on :session-start
       (lambda (ctx data)
         (declare (ignore data))
         (pig:ctx-set-status ctx "autolith" "autolith bridge ready"))))
