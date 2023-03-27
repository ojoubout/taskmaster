import signal


class Program:
    count = 0
    def __init__(self, command, numprocs=1, umask='022', directory='.', autostart=True, autorestart='unexpected', startsecs=1,
                startretries=3, stopsignal=signal.SIGTERM, stopwaitsecs=10, env={}, **kwargs) -> None:
        Program.count = Program.count + 1
        self.command = command
        self.numprocs = numprocs
        self.umask = umask
        self.directory = directory
        self.autostart = autostart
        self.autorestart = autorestart
        self.startsecs = startsecs
        self.startretries = startretries
        self.stopsignal = stopsignal
        self.stopwaitsecs = stopwaitsecs
        self.stdout = f'/var/log/taskmaster_{Program.count}_out.log'
        self.stderr = f'/var/log/taskmaster_{Program.count}_err.log'
        self.env = env
