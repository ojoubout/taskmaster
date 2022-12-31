import signal


class Program:
    count = 0
    def __init__(self, command) -> None:
        Program.count = Program.count + 1
        self.command = command
        self.numprocs = 1
        self.umask = '022'
        self.directory = '.'
        self.autostart = True
        self.autorestart = 'unexpected'
        self.startsecs = 1
        self.startretries = 3
        self.stopsignal = signal.SIGTERM
        self.stopwaitsecs = 10
        self.stdout = f'/var/log/taskmaster_{Program.count}_out.log'
        self.stderr = f'/var/log/taskmaster_{Program.count}_err.log'
        self.env = {}